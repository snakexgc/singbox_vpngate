package vpngateruntime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"guiforcores/internal/guardian"
	"guiforcores/internal/vpngate"
)

type Protection interface {
	Arm(context.Context, guardian.Policy) error
	Disarm(context.Context) error
	Status() guardian.ProtectionStatus
}

type Manager struct {
	mu             sync.Mutex
	op             sync.Mutex
	dir            string
	guard          Protection
	settings       vpngate.Settings
	pool           *vpngate.Pool
	selected       string
	runtime        vpngate.RuntimeState
	rules          vpngate.RulesState
	jobs           map[string]*vpngate.Job
	cancels        map[string]context.CancelFunc
	ctx            context.Context
	cancel         context.CancelFunc
	process        *coreProcess
	proxySnapshot  *systemProxySnapshot
	desktop        bool
	corePath       string
	selectedCore   string
	coreHTTPClient func(vpngate.Upstream) (*http.Client, error)
	isAdmin        func() bool
	restoreProxy   func() error
	applyProxy     func(int) error
	resolveOwner   func(context.Context, string, int) ([]string, error)
	repair         vpngate.RepairState
	repairing      bool
	stopping       bool
	closed         bool
	jobDone        map[string]chan struct{}
}

type persisted struct {
	Settings      vpngate.Settings     `json:"settings"`
	Nodes         []vpngate.Node       `json:"nodes"`
	Selected      string               `json:"selectedNodeId"`
	Armed         bool                 `json:"armed"`
	Proxy         *systemProxySnapshot `json:"proxy,omitempty"`
	RuntimeActive bool                 `json:"runtimeActive,omitempty"`
	SelectedCore  string               `json:"selectedCore,omitempty"`
}

func New(dataDir string, guard Protection) (*Manager, error) {
	return newManager(dataDir, guard, "", false)
}

// NewDesktop owns the GUI's portable core and never installs or arms protection.
func NewDesktop(dataDir, corePath string) (*Manager, error) {
	return newManager(dataDir, nil, corePath, true)
}

func newManager(dataDir string, guard Protection, corePath string, desktop bool) (*Manager, error) {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, err
	}
	if err := seedRules(dataDir); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{dir: dataDir, guard: guard, settings: vpngate.DefaultSettings(), pool: vpngate.NewPool(nil), runtime: vpngate.RuntimeState{Status: "stopped"}, jobs: map[string]*vpngate.Job{}, cancels: map[string]context.CancelFunc{}, ctx: ctx, cancel: cancel}
	m.desktop, m.corePath, m.isAdmin = desktop, corePath, desktopElevated
	m.coreHTTPClient = proxyClient
	m.jobDone = map[string]chan struct{}{}
	m.restoreProxy, m.applyProxy = m.restoreSystemProxy, m.applySystemProxy
	m.resolveOwner = resolveUpstreamOwner
	if desktop {
		m.settings.CorePath = corePath
	} else {
		m.settings.CorePath = guardian.ManagedCorePath()
	}
	data, err := os.ReadFile(filepath.Join(dataDir, "state.json"))
	if err == nil {
		var p persisted
		if err = json.Unmarshal(data, &p); err != nil {
			cancel()
			return nil, fmt.Errorf("读取 VPNGate 状态：%w", err)
		}
		m.settings = p.Settings
		if desktop && p.SelectedCore != "" {
			selected, selectionErr := ownedCorePath(dataDir, p.SelectedCore)
			if selectionErr != nil {
				cancel()
				return nil, selectionErr
			}
			m.selectedCore, m.corePath = p.SelectedCore, selected
		}
		for i := range p.Nodes {
			if p.Nodes[i].Status == "testing" {
				p.Nodes[i].Status = "untested"
				p.Nodes[i].LatencyMs = nil
				p.Nodes[i].HandshakeMs = nil
			}
		}
		m.pool = vpngate.NewPool(p.Nodes)
		m.selected = p.Selected
		m.proxySnapshot = p.Proxy
		if desktop && (p.Armed || p.RuntimeActive || p.Proxy != nil) {
			m.repair = vpngate.RepairState{Needed: true, Message: "上次运行未正常停止；请修复软件以恢复本应用的网络设置"}
		} else if p.Armed {
			m.runtime.Status = "protected"
			m.runtime.Message = "上次接管未正常停止；保护保持，等待手动重新连接或停止接管"
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		cancel()
		return nil, err
	}
	if desktop {
		m.settings.CorePath = m.corePath
		if err = m.inspectDesktopRecovery(); err != nil {
			cancel()
			return nil, err
		}
	}
	m.inspectRulesLocked()
	if guard != nil || desktop {
		go m.schedule()
	}
	return m, nil
}

func (m *Manager) Close() {
	if m.desktop {
		m.mu.Lock()
		m.closed = true
		m.mu.Unlock()
		m.cancel()
		m.cancelAndWaitJobs()
		m.op.Lock()
		defer m.op.Unlock()
		err := m.stop(context.Background())
		if err == nil {
			err = m.cleanRuntimeFiles()
		}
		if err != nil {
			m.markRepair(err)
		}
		return
	}
	m.cancel()
	m.op.Lock()
	defer m.op.Unlock()
	m.mu.Lock()
	p := m.process
	m.process = nil
	m.mu.Unlock()
	if p != nil {
		p.stop()
	} /* crashes/shutdown never disarm persistent protection */
}

func (m *Manager) stateLocked() vpngate.State {
	nodes := m.pool.Snapshot()
	for i := range nodes {
		nodes[i].Config = ""
	}
	jobs := make([]vpngate.Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		jobs = append(jobs, *j)
	}
	protected := false
	if m.guard != nil {
		protected = m.guard.Status().Protected
	}
	m.runtime.Protected = protected
	m.runtime.RequiresAdmin = m.desktop && m.settings.TUNEnabled && !m.isAdmin()
	return vpngate.State{Settings: m.settings, Nodes: nodes, SelectedNodeID: m.selected, Runtime: m.runtime, Rules: m.rules, Jobs: jobs, Repair: m.repair, Service: vpngate.ServiceState{Installed: m.guard != nil, Running: m.guard != nil, Protected: protected, RequiresAdmin: !m.desktop && m.guard == nil}}
}
func (m *Manager) State() vpngate.State { m.mu.Lock(); defer m.mu.Unlock(); return m.stateLocked() }
func (m *Manager) persistLocked() error {
	p := persisted{Settings: m.settings, Nodes: m.pool.Snapshot(), Selected: m.selected, SelectedCore: m.selectedCore, Armed: !m.desktop && m.runtime.Status != "stopped" && m.settings.RoutingPreset != "direct", Proxy: m.proxySnapshot, RuntimeActive: m.desktop && (m.process != nil || m.runtime.Status == "starting" || m.repair.Needed)}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(m.dir, "state.json"), data)
}
func atomicWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return replaceFile(name, path)
}
func id() string  { var b [12]byte; _, _ = rand.Read(b[:]); return hex.EncodeToString(b[:]) }
func now() string { return time.Now().UTC().Format(time.RFC3339) }

// RestoreProxySnapshot is the explicit administrator recovery path. Its separate
// snapshot remains readable even if the main pool/state file is corrupt.
func RestoreProxySnapshot(dataDir string) error {
	data, err := os.ReadFile(filepath.Join(dataDir, "proxy-snapshot.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var snapshot systemProxySnapshot
	if err = json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("系统代理恢复快照损坏：%w", err)
	}
	m := &Manager{dir: dataDir, proxySnapshot: &snapshot}
	return m.restoreSystemProxy()
}

func (m *Manager) Handle(ctx context.Context, method string, payload json.RawMessage) (any, error) {
	switch method {
	case "core.status":
		return m.coreStatus(ctx), nil
	case "core.releases":
		return m.listCoreReleases(ctx)
	case "core.install", "core.use":
		var p struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, err
		}
		if method == "core.use" {
			return m.useCore(ctx, p.Version)
		}
		return m.startCoreInstall(p.Version)
	case "state":
		return m.State(), nil
	case "settings.update":
		var s vpngate.Settings
		if err := json.Unmarshal(payload, &s); err != nil {
			return nil, err
		}
		if err := vpngate.ValidateSettings(s); err != nil {
			return nil, err
		}
		m.op.Lock()
		defer m.op.Unlock()
		m.mu.Lock()
		if m.coreInstallingLocked() {
			m.mu.Unlock()
			return nil, fmt.Errorf("内核正在安装，请完成或取消后重试")
		}
		old := m.settings
		active := m.activeNodeLocked()
		// The service executes only its ACL-protected binary, never arbitrary editor paths.
		if m.guard != nil {
			s.CorePath = guardian.ManagedCorePath()
		} else if m.desktop {
			s.CorePath = m.corePath
		}
		m.settings = s
		m.pool.Optimize(s.PoolCapacity, active)
		m.reconcileSelectionLocked()
		if fingerprint(old) != fingerprint(s) {
			for _, n := range m.pool.Snapshot() {
				m.pool.UpdateProbe(vpngate.ProbeResult{NodeID: n.ID, Status: "untested"})
			}
		}
		running := m.process != nil
		err := m.persistLocked()
		m.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if running {
			if err = m.connect(ctx); err != nil {
				return nil, err
			}
		}
		return m.State(), nil
	case "nodes.use":
		var p struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, err
		}
		m.op.Lock()
		defer m.op.Unlock()
		m.mu.Lock()
		if m.coreInstallingLocked() {
			m.mu.Unlock()
			return nil, fmt.Errorf("内核正在安装，请完成或取消后重试")
		}
		found := false
		for _, n := range m.pool.Snapshot() {
			if n.ID == p.ID {
				found = true
				break
			}
		}
		if !found {
			m.mu.Unlock()
			return nil, fmt.Errorf("节点不存在")
		}
		m.selected = p.ID
		running := m.process != nil
		err := m.persistLocked()
		m.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if running {
			if err = m.connect(ctx); err != nil {
				return nil, err
			}
		}
		return m.State(), nil
	case "nodes.deleteFailed", "nodes.delete":
		m.mu.Lock()
		defer m.mu.Unlock()
		if method == "nodes.deleteFailed" {
			m.pool.DeleteFailed(m.activeNodeLocked())
		} else {
			var p struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(payload, &p); err != nil {
				return nil, err
			}
			if err := m.pool.Delete(p.ID, m.activeNodeLocked()); err != nil {
				return nil, err
			}
		}
		exists := false
		for _, n := range m.pool.Snapshot() {
			if n.ID == m.selected {
				exists = true
			}
		}
		if !exists {
			m.selected = ""
		}
		if err := m.persistLocked(); err != nil {
			return nil, err
		}
		return m.stateLocked(), nil
	case "connect":
		m.op.Lock()
		defer m.op.Unlock()
		if m.guard == nil && !m.desktop {
			return nil, fmt.Errorf("请先安装并启动 Windows 保护服务")
		}
		if err := m.connect(ctx); err != nil {
			return nil, err
		}
		return m.State(), nil
	case "repair":
		return m.Repair(ctx)
	case "stop":
		if m.desktop {
			return m.stopDesktop(ctx)
		}
		m.op.Lock()
		defer m.op.Unlock()
		if err := m.stop(ctx); err != nil {
			return nil, err
		}
		return m.State(), nil
	case "tests.start":
		var p struct {
			IDs []string `json:"ids"`
		}
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, err
		}
		return m.startTests(p.IDs)
	case "nodes.refresh":
		var p struct {
			Manual bool `json:"manual"`
		}
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &p); err != nil {
				return nil, err
			}
		}
		return m.startRefresh(p.Manual)
	case "jobs.cancel":
		var p struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, err
		}
		m.mu.Lock()
		c := m.cancels[p.ID]
		m.mu.Unlock()
		if c != nil {
			c()
		}
		return m.State(), nil
	case "rules.update":
		return m.startRules()
	default:
		return nil, fmt.Errorf("未知 VPNGate 操作：%s", method)
	}
}
func (m *Manager) reconcileSelectionLocked() {
	if m.selected == "" {
		return
	}
	for _, n := range m.pool.Snapshot() {
		if n.ID == m.selected {
			return
		}
	}
	m.selected = ""
}
func (m *Manager) activeNodeLocked() string {
	if m.runtime.ConnectedNodeID != "" {
		return m.runtime.ConnectedNodeID
	}
	if m.runtime.Status == "starting" {
		return m.selected
	}
	return ""
}

func (m *Manager) beginJob(kind string) (context.Context, *vpngate.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.repairing || m.stopping || m.ctx.Err() != nil {
		return nil, nil, fmt.Errorf("软件正在停止或修复，请稍后重试")
	}
	if kind == "core" && (m.process != nil || m.runtime.Status == "starting" || m.runtime.Status == "connected" || m.runtime.Status == "direct" || m.runtime.Status == "degraded" || m.runtime.Status == "protected") {
		return nil, nil, fmt.Errorf("请先停止连接，再安装或切换内核")
	}
	for _, j := range m.jobs {
		if j.Status == "running" && (j.Kind == "core" || kind == "core" || j.Kind == kind || (j.Kind == "refresh" && kind == "test") || (j.Kind == "test" && kind == "refresh")) {
			return nil, nil, fmt.Errorf("相关任务正在运行，请完成或取消后重试")
		}
	}
	ctx, cancel := context.WithCancel(m.ctx)
	j := &vpngate.Job{ID: id(), Kind: kind, Status: "running", StartedAt: now()}
	m.jobs[j.ID] = j
	m.cancels[j.ID] = cancel
	m.jobDone[j.ID] = make(chan struct{})
	if len(m.jobs) > 30 {
		for k, v := range m.jobs {
			if v.Status != "running" {
				delete(m.jobs, k)
				break
			}
		}
	}
	return ctx, j, nil
}
func (m *Manager) finishJob(j *vpngate.Job, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if errors.Is(err, context.Canceled) {
		j.Status = "canceled"
	} else if err != nil {
		j.Status = "failed"
		j.Message = err.Error()
	} else {
		j.Status = "completed"
	}
	j.FinishedAt = now()
	delete(m.cancels, j.ID)
	if e := m.persistLocked(); e != nil {
		j.Status = "failed"
		j.Message = e.Error()
	}
	if done := m.jobDone[j.ID]; done != nil {
		close(done)
		delete(m.jobDone, j.ID)
	}
}

func (m *Manager) schedule() {
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	lastRefresh := time.Time{}
	lastRules := time.Time{}
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-timer.C:
			m.mu.Lock()
			s := m.settings
			count := len(m.pool.Snapshot())
			ready := s.Upstream.Port > 0
			m.mu.Unlock()
			coreInfo, coreErr := os.Stat(s.CorePath)
			ready = ready && coreErr == nil && coreInfo.Mode().IsRegular()
			if ready && count < s.PoolCapacity && time.Since(lastRefresh) >= time.Duration(s.RefreshMinutes)*time.Minute {
				if _, err := m.startRefresh(false); err == nil {
					lastRefresh = time.Now()
				}
			}
			if ready && s.RulesAutoUpdate && time.Since(lastRules) >= time.Duration(s.RulesUpdateHours)*time.Hour {
				if _, err := m.startRules(); err == nil {
					lastRules = time.Now()
				}
			}
			timer.Reset(time.Minute)
		}
	}
}
