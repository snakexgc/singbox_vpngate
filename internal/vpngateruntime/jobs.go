package vpngateruntime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"guiforcores/internal/vpngate"
)

const feedURL = "https://www.vpngate.net/api/iphone/"

func (m *Manager) startRefresh(manual bool) (vpngate.Job, error) {
	ctx, j, err := m.beginJob("refresh")
	if err != nil {
		return vpngate.Job{}, err
	}
	initial := *j
	go func() { err := m.refresh(ctx, j, manual); m.finishJob(j, err) }()
	return initial, nil
}
func (m *Manager) refresh(ctx context.Context, j *vpngate.Job, manual bool) error {
	m.mu.Lock()
	s := m.settings
	old := m.pool.Snapshot()
	m.mu.Unlock()
	client, err := proxyClient(s.Upstream)
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	data, err := download(ctx, client, feedURL, 16<<20)
	if err != nil {
		return err
	}
	nodes, err := vpngate.ParseCSV(bytes.NewReader(data))
	if err != nil {
		return err
	}
	if len(nodes) == 0 {
		return fmt.Errorf("VPNGate API 未提供可用的 OpenVPN TCP 配置；原节点池保持不变")
	}
	for i := range nodes {
		nodes[i].LastSeen = now()
	}
	if !manual {
		m.mu.Lock()
		defer m.mu.Unlock()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		m.pool.Merge(nodes, s.PoolCapacity, m.activeNodeLocked())
		j.Total = len(nodes)
		j.Completed = len(nodes)
		return m.persistLocked()
	}
	if err = preflight(ctx, s.Upstream); err != nil {
		return err
	}
	if _, err = validateCore(ctx, s.CorePath); err != nil {
		return err
	}
	known := map[string]vpngate.Node{}
	for _, n := range old {
		known[n.ID] = n
	}
	candidates := []vpngate.Node{}
	for _, n := range nodes {
		previous, exists := known[n.ID]
		if !exists || !previous.SameProfile(n) {
			candidates = append(candidates, n)
		}
	}
	toTest := refreshProbeTargets(candidates, old, s)
	results, batchErr := m.probeBatch(ctx, j, s, toTest)
	if batchErr != nil {
		return batchErr
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if fingerprint(s) != fingerprint(m.settings) {
		return fmt.Errorf("上游设置已变化，未替换节点池")
	}
	combined := m.pool.Snapshot()
	indexes := map[string]int{}
	for i, n := range combined {
		indexes[n.ID] = i
	}
	for _, n := range candidates {
		r, ok := results[n.ID]
		if !ok || r.Status != "available" {
			continue
		}
		applyToNode(&n, r)
		if i, exists := indexes[n.ID]; exists {
			if n.ID != m.activeNodeLocked() {
				combined[i] = n
			}
		} else {
			combined = append(combined, n)
		}
	}
	m.pool = vpngate.NewPool(combined)
	m.pool.Optimize(m.settings.PoolCapacity, m.activeNodeLocked())
	m.reconcileSelectionLocked()
	return m.persistLocked()
}
func (m *Manager) startTests(ids []string) (vpngate.Job, error) {
	// Reserve from the same operation boundary as installation and activation,
	// so a queued test cannot capture the previous core just before a switch.
	m.op.Lock()
	defer m.op.Unlock()
	m.mu.Lock()
	s := m.settings
	all := m.pool.Snapshot()
	m.mu.Unlock()
	if err := vpngate.ValidateSettings(s); err != nil {
		return vpngate.Job{}, err
	}
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	nodes := []vpngate.Node{}
	for _, n := range all {
		if len(ids) == 0 || wanted[n.ID] {
			nodes = append(nodes, n)
		}
	}
	if len(nodes) == 0 {
		return vpngate.Job{}, fmt.Errorf("没有可测试的节点")
	}
	ctx, j, err := m.beginJob("test")
	if err != nil {
		return vpngate.Job{}, err
	}
	m.mu.Lock()
	j.Total = len(nodes)
	initial := *j
	m.mu.Unlock()
	go func() {
		err := preflight(ctx, s.Upstream)
		if err == nil {
			_, err = validateCore(ctx, s.CorePath)
		}
		if err == nil {
			_, err = m.probeBatch(ctx, j, s, nodes)
			if err == nil {
				err = ctx.Err()
			}
		}
		m.finishJob(j, err)
	}()
	return initial, nil
}
func (m *Manager) probeBatch(parent context.Context, j *vpngate.Job, s vpngate.Settings, nodes []vpngate.Node) (map[string]vpngate.ProbeResult, error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	m.mu.Lock()
	j.Total = len(nodes)
	m.mu.Unlock()
	queue := make(chan vpngate.Node)
	results := map[string]vpngate.ProbeResult{}
	var resultMu sync.Mutex
	var batchErr error
	var wg sync.WaitGroup
	for i := 0; i < s.TestConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range queue {
				if ctx.Err() != nil {
					return
				}
				m.mu.Lock()
				for _, current := range m.pool.Snapshot() {
					if current.ID == n.ID && current.SameProfile(n) {
						m.pool.UpdateProbe(vpngate.ProbeResult{NodeID: n.ID, Status: "testing"})
						break
					}
				}
				m.mu.Unlock()
				r := m.probe(ctx, s, n)
				if r.FailureKind == "environment" && ctx.Err() == nil {
					resultMu.Lock()
					if batchErr == nil {
						batchErr = fmt.Errorf("测试环境故障：%s", r.Error)
					}
					resultMu.Unlock()
					cancel()
				}
				if r.Status == "failed" && ctx.Err() == nil {
					if upstreamErr := preflight(ctx, s.Upstream); upstreamErr != nil {
						resultMu.Lock()
						if batchErr == nil {
							batchErr = upstreamErr
						}
						resultMu.Unlock()
						cancel()
						r.Status = "untested"
						r.FailureKind = "environment"
						r.Error = "无法连接配置的外部 mixed 监听端口，已停止测试"
					}
				}
				resultMu.Lock()
				results[n.ID] = r
				resultMu.Unlock()
				m.mu.Lock()
				if fingerprint(s) == fingerprint(m.settings) {
					for _, current := range m.pool.Snapshot() {
						if current.ID == n.ID && current.SameProfile(n) {
							if r.FailureKind == "environment" || r.Status == "canceled" && parent.Err() == nil {
								m.pool.UpdateProbe(resultFromNode(n))
							} else {
								m.pool.UpdateProbe(r)
							}
							break
						}
					}
				}
				j.Completed++
				if r.Status == "available" {
					j.Available++
				} else if r.Status == "failed" {
					j.Failed++
				}
				m.mu.Unlock()
			}
		}()
	}
sendLoop:
	for _, n := range nodes {
		select {
		case queue <- n:
		case <-ctx.Done():
			break sendLoop
		}
	}
	close(queue)
	wg.Wait()
	return results, batchErr
}
func (m *Manager) probe(parent context.Context, s vpngate.Settings, n vpngate.Node) (r vpngate.ProbeResult) {
	r = resultForEgressPath(s, n)
	r.NodeID, r.Status, r.FailureKind = n.ID, "failed", "node"
	r.LatencyMs, r.HandshakeMs = nil, nil
	r.PathFingerprint, r.LastTested, r.Error = fingerprint(s), now(), ""
	defer func() {
		if parent.Err() != nil {
			r.Status = "canceled"
			r.FailureKind = "canceled"
			r.Error = "测试已取消"
		}
	}()
	ctx, cancel := context.WithTimeout(parent, time.Duration(s.TestTimeoutSeconds)*time.Second)
	defer cancel()
	if err := vpngate.ValidateSettings(s); err != nil {
		r.Error = err.Error()
		r.FailureKind = "environment"
		r.Status = "untested"
		return
	}
	endpoint, err := vpngate.ParseOpenVPN(n.Config)
	if err != nil {
		r.Error = fmt.Sprintf("node profile: %v", err)
		return
	}
	target := net.JoinHostPort(endpoint["server"].(string), fmt.Sprint(endpoint["server_port"]))
	if err = checkProxyAccess(ctx, s.Upstream, target); err != nil {
		r.Error = err.Error()
		r.FailureKind = "environment"
		r.Status = "untested"
		return
	}
	if err = ctx.Err(); err != nil {
		r.Error = fmt.Sprintf("目标节点代理 CONNECT 检查超时：%v", err)
		return
	}
	port, err := freePort()
	if err != nil {
		r.Error = err.Error()
		r.FailureKind = "environment"
		r.Status = "untested"
		return
	}
	if s.Upstream.Host == "127.0.0.1" && port == s.Upstream.Port {
		r.Error = "测试端口与上游冲突"
		r.FailureKind = "environment"
		r.Status = "untested"
		return
	}
	dir := filepath.Join(m.dir, "probes", id())
	defer removeProbeDir(filepath.Join(m.dir, "probes"), dir)
	opts := vpngate.ConfigOptions{MixedPort: port, LogPath: filepath.Join(dir, "core.log")}
	p, err := startCore(ctx, s, &n, opts, dir, nil)
	if err != nil {
		r.Error = coreFailureDiagnostic(err, dir, s.Upstream)
		var nodeErr nodeConfigError
		if !errors.As(err, &nodeErr) {
			r.Status = "untested"
			r.FailureKind = "environment"
		}
		return
	}
	defer p.stop()
	err = waitListener(ctx, p, port)
	var handshake int64
	if err == nil {
		handshake, err = tunnelReady(ctx, p, port, s.TestURL)
	}
	if err != nil {
		r.Error = coreFailureDiagnostic(err, dir, s.Upstream)
		return
	}
	r.HandshakeMs = &handshake
	client, err := proxyClient(vpngate.Upstream{Host: "127.0.0.1", Port: port, Protocol: "http"})
	if err != nil {
		r.Error = err.Error()
		return
	}
	defer client.CloseIdleConnections()
	client.Timeout = 5 * time.Second
	times := []int64{}
	for i := 0; i < 3; i++ {
		ms, e := measureRequest(ctx, client, s.TestURL)
		if e != nil {
			r.Error = coreFailureDiagnostic(fmt.Errorf("目标网站连通性测试失败：%w", e), dir, s.Upstream)
			return
		}
		times = append(times, ms)
	}
	slices.Sort(times)
	r.LatencyMs = &times[1]
	r.Status = "available"
	r.FailureKind = ""
	r.Error = ""
	// Location lookup has its own bounded timeout and still honors the
	// batch's cancellation. Connectivity can consume its full allotted time.
	cancel()
	enrichEgress(parent, client, s, n, &r)
	return
}
func refreshProbeTargets(candidates, existing []vpngate.Node, s vpngate.Settings) []vpngate.Node {
	targets := append([]vpngate.Node{}, candidates...)
	queued := map[string]bool{}
	for _, n := range candidates {
		queued[n.ID] = true
	}
	for _, n := range existing {
		if queued[n.ID] {
			continue
		}
		tested, _ := time.Parse(time.RFC3339, n.LastTested)
		egressMissing := n.Status == "available" && n.EgressIP == "" && n.EgressError == ""
		if egressMissing || n.Status != "available" && n.Status != "failed" || time.Since(tested) > 30*time.Minute || n.PathFingerprint != fingerprint(s) {
			targets = append(targets, n)
			queued[n.ID] = true
		}
	}
	return targets
}
func resultFromNode(n vpngate.Node) vpngate.ProbeResult {
	return vpngate.ProbeResult{NodeID: n.ID, Status: n.Status, LatencyMs: n.LatencyMs, HandshakeMs: n.HandshakeMs, LastTested: n.LastTested, Error: n.Error, FailureKind: n.FailureKind, PathFingerprint: n.PathFingerprint, EgressIP: n.EgressIP, EgressCountry: n.EgressCountry, EgressCountryCode: n.EgressCountryCode, EgressRegion: n.EgressRegion, EgressCity: n.EgressCity, EgressError: n.EgressError, EgressCheckedAt: n.EgressCheckedAt, EgressLanguage: n.EgressLanguage, EgressCacheKey: n.EgressCacheKey}
}
func applyToNode(n *vpngate.Node, r vpngate.ProbeResult) {
	n.Status = r.Status
	n.LatencyMs = r.LatencyMs
	n.HandshakeMs = r.HandshakeMs
	n.LastTested = r.LastTested
	n.Error = r.Error
	n.FailureKind = r.FailureKind
	n.PathFingerprint = r.PathFingerprint
	n.EgressIP = r.EgressIP
	n.EgressCountry = r.EgressCountry
	n.EgressCountryCode = r.EgressCountryCode
	n.EgressRegion = r.EgressRegion
	n.EgressCity = r.EgressCity
	n.EgressError = r.EgressError
	n.EgressCheckedAt = r.EgressCheckedAt
	n.EgressLanguage = r.EgressLanguage
	n.EgressCacheKey = r.EgressCacheKey
}
func removeProbeDir(root, path string) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return
	}
	_ = os.RemoveAll(path)
}

func (m *Manager) inspectRulesLocked() {
	for name, status := range map[string]*vpngate.RuleStatus{"cn": &m.rules.CN, "gfw": &m.rules.GFW} {
		info, err := os.Stat(filepath.Join(m.dir, "rules", name+".srs"))
		status.Ready = err == nil && info.Size() > 0
		if status.Ready {
			status.UpdatedAt = info.ModTime().UTC().Format(time.RFC3339)
		}
	}
}
func (m *Manager) startRules() (vpngate.Job, error) {
	ctx, j, err := m.beginJob("rules")
	if err != nil {
		return vpngate.Job{}, err
	}
	m.mu.Lock()
	j.Total = 2
	initial := *j
	s := m.settings
	m.mu.Unlock()
	go func() { err := m.updateRules(ctx, j, s); m.finishJob(j, err) }()
	return initial, nil
}
func (m *Manager) updateRules(ctx context.Context, j *vpngate.Job, s vpngate.Settings) error {
	client, err := proxyClient(s.Upstream)
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	if _, err = validateCore(ctx, s.CorePath); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Join(m.dir, "rules"), 0700); err != nil {
		return err
	}
	for _, item := range []struct{ name, url string }{{"cn", s.CNRulesURL}, {"gfw", s.GFWRulesURL}} {
		err = m.updateRule(ctx, client, s, item.name, item.url)
		m.mu.Lock()
		status := &m.rules.CN
		if item.name == "gfw" {
			status = &m.rules.GFW
		}
		if err != nil {
			status.Error = err.Error()
		} else {
			status.Ready = true
			status.Error = ""
			status.UpdatedAt = now()
			j.Completed++
		}
		m.mu.Unlock()
		if err != nil {
			return err
		}
	}
	// Rule changes may require restarting the core; the same node and WFP remain.
	m.op.Lock()
	defer m.op.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	m.mu.Lock()
	running := m.process != nil
	m.mu.Unlock()
	if running {
		return m.connect(ctx)
	}
	return nil
}
func (m *Manager) updateRule(ctx context.Context, client *http.Client, s vpngate.Settings, name, address string) error {
	data, err := download(ctx, client, address, 32<<20)
	if err != nil {
		return err
	}
	dir := filepath.Join(m.dir, "rules")
	f, err := os.CreateTemp(dir, ".rules-*.srs")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	// Decompilation uses the pinned parser, rather than trusting a filename/header.
	output := tmp + ".json"
	defer os.Remove(output)
	check := exec.CommandContext(ctx, s.CorePath, "rule-set", "decompile", tmp, "-o", output)
	hideCommand(check)
	if out, e := check.CombinedOutput(); e != nil {
		return fmt.Errorf("%s 规则校验失败：%s", name, strings.TrimSpace(string(out)))
	}
	target := filepath.Join(dir, name+".srs")
	if previous, e := os.ReadFile(target); e == nil {
		if err = atomicWrite(target+".previous", previous); err != nil {
			return err
		}
	}
	return replaceFile(tmp, target)
}
