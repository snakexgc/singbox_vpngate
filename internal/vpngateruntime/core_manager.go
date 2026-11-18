package vpngateruntime

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"guiforcores/internal/vpngate"
)

const coreReleasesURL = "https://api.github.com/repos/SagerNet/sing-box/releases"
const coreMetadataLimit int64 = 32 << 20
const coreArchiveLimit int64 = 128 << 20
const coreExtractLimit int64 = 384 << 20

var coreSemver = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*))?(?:\+[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$`)
var coreVersionLine = regexp.MustCompile(`(?m)^sing-box version ([^\s]+)`)
var coreSelectionPattern = regexp.MustCompile(`^cores/(v[0-9A-Za-z.+-]+)-[0-9a-f]{24}/sing-box\.exe$`)

type coreRelease struct {
	Version     string `json:"version"`
	Prerelease  bool   `json:"prerelease"`
	PublishedAt string `json:"publishedAt"`
}
type githubCoreRelease struct {
	Tag         string `json:"tag_name"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
	PublishedAt string `json:"published_at"`
	Assets      []struct {
		Name   string `json:"name"`
		URL    string `json:"browser_download_url"`
		Digest string `json:"digest"`
		Size   int64  `json:"size"`
	} `json:"assets"`
}
type installedCore struct {
	Version  string `json:"version"`
	Path     string `json:"path"`
	Selected bool   `json:"selected"`
}
type coreStatus struct {
	Path              string          `json:"path"`
	Version           string          `json:"version"`
	Installed         bool            `json:"installed"`
	Error             string          `json:"error"`
	InstalledVersions []installedCore `json:"installedVersions"`
}

func canonicalCoreVersion(raw string) (string, error) {
	version := strings.TrimPrefix(strings.TrimSpace(raw), "v")
	matches := coreSemver.FindStringSubmatch(version)
	if matches == nil {
		return "", fmt.Errorf("请输入有效的 sing-box 语义版本，例如 v1.14.4")
	}
	for _, identifier := range strings.Split(matches[4], ".") {
		if len(identifier) > 1 && identifier[0] == '0' && strings.Trim(identifier, "0123456789") == "" {
			return "", fmt.Errorf("语义版本的预发行数字不能以零开头")
		}
	}
	major, e1 := strconv.ParseUint(matches[1], 10, 32)
	minor, e2 := strconv.ParseUint(matches[2], 10, 32)
	if e1 != nil || e2 != nil || major < 1 || major == 1 && (minor < 14 || minor == 14 && matches[3] == "0" && matches[4] != "") {
		return "", fmt.Errorf("原生 OpenVPN 需要 sing-box 1.14.0 或更新版本")
	}
	return "v" + version, nil
}

func parseCoreVersion(output string) (string, error) {
	match := coreVersionLine.FindStringSubmatch(output)
	if len(match) != 2 {
		return "", fmt.Errorf("无法解析 sing-box 版本")
	}
	version, err := canonicalCoreVersion(match[1])
	if err != nil {
		return "", err
	}
	tags := map[string]bool{}
	for _, field := range strings.FieldsFunc(output, func(r rune) bool { return r == ',' || r == ' ' || r == '\r' || r == '\n' || r == '\t' }) {
		tags[field] = true
	}
	if !tags["with_openvpn"] {
		return "", fmt.Errorf("内核缺少 with_openvpn")
	}
	// 1.14 uses gVisor for the native OpenVPN stack. Official 1.15 builds
	// provide sing-tun's own stack and no longer include the with_gvisor tag.
	parts := coreSemver.FindStringSubmatch(strings.TrimPrefix(version, "v"))
	if parts[1] == "1" && parts[2] == "14" && !tags["with_gvisor"] {
		return "", fmt.Errorf("sing-box 1.14 内核缺少 with_gvisor")
	}
	return strings.TrimPrefix(version, "v"), nil
}

func ownedCorePath(dir, selection string) (string, error) {
	if !coreSelectionPattern.MatchString(selection) {
		return "", fmt.Errorf("受管理的内核选择路径无效")
	}
	match := coreSelectionPattern.FindStringSubmatch(selection)
	if _, err := canonicalCoreVersion(match[1]); err != nil {
		return "", err
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	target := filepath.Join(root, filepath.FromSlash(selection))
	// Reject reparse points/symlinks in every owned component, including the exe.
	current := root
	for _, component := range strings.Split(selection, "/") {
		current = filepath.Join(current, component)
		info, e := os.Lstat(current)
		if e != nil {
			if os.IsNotExist(e) {
				break
			}
			return "", e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("受管理的内核路径不能包含符号链接")
		}
	}
	return target, nil
}

func (m *Manager) installedCoresLocked() []installedCore {
	installed := []installedCore{}
	entries, _ := os.ReadDir(filepath.Join(m.dir, "cores"))
	for _, entry := range entries {
		selection := "cores/" + entry.Name() + "/sing-box.exe"
		match := coreSelectionPattern.FindStringSubmatch(selection)
		if !entry.IsDir() || len(match) != 2 {
			continue
		}
		candidate, err := ownedCorePath(m.dir, selection)
		if err != nil {
			continue
		}
		info, err := os.Stat(candidate)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		installed = append(installed, installedCore{Version: match[1], Path: candidate, Selected: candidate == m.corePath})
	}
	sort.Slice(installed, func(i, j int) bool { return installed[i].Version > installed[j].Version })
	return installed
}

func (m *Manager) coreStatus(ctx context.Context) coreStatus {
	m.mu.Lock()
	status := coreStatus{Path: m.settings.CorePath, InstalledVersions: m.installedCoresLocked()}
	m.mu.Unlock()
	version, err := validateCore(ctx, status.Path)
	if err != nil {
		status.Error = err.Error()
	} else {
		status.Version, status.Installed = version, true
	}
	return status
}

func (m *Manager) coreInstallingLocked() bool {
	for _, job := range m.jobs {
		if job.Kind == "core" && job.Status == "running" {
			return true
		}
	}
	return false
}

func (m *Manager) listCoreReleases(ctx context.Context) (any, error) {
	m.mu.Lock()
	upstream := m.settings.Upstream
	m.mu.Unlock()
	client, err := m.coreHTTPClient(upstream)
	if err != nil {
		return nil, err
	}
	defer client.CloseIdleConnections()
	data, err := coreMetadata(ctx, client, coreReleasesURL+"?per_page=100")
	if err != nil {
		return nil, err
	}
	var official []githubCoreRelease
	if err = json.Unmarshal(data, &official); err != nil {
		return nil, fmt.Errorf("官方版本列表无效：%w", err)
	}
	releases := []coreRelease{}
	for _, release := range official {
		version, err := canonicalCoreVersion(release.Tag)
		if err == nil && !release.Draft {
			releases = append(releases, coreRelease{Version: version, Prerelease: release.Prerelease, PublishedAt: release.PublishedAt})
		}
	}
	return struct {
		Releases []coreRelease `json:"releases"`
	}{releases}, nil
}

func coreMetadata(ctx context.Context, client *http.Client, address string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "VPNGate-Desktop")
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("通过外部上游读取 sing-box 官方版本失败：%w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("官方版本请求失败：HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, coreMetadataLimit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > coreMetadataLimit {
		return nil, fmt.Errorf("官方版本元数据超过大小限制")
	}
	return data, nil
}

func (m *Manager) startCoreInstall(rawVersion string) (vpngate.Job, error) {
	if !m.desktop {
		return vpngate.Job{}, fmt.Errorf("内核下载仅适用于独立桌面模式")
	}
	version, err := canonicalCoreVersion(rawVersion)
	if err != nil {
		return vpngate.Job{}, err
	}
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		return vpngate.Job{}, fmt.Errorf("当前仅支持官方 Windows amd64 内核发行包")
	}
	m.op.Lock()
	defer m.op.Unlock()
	ctx, job, err := m.beginJob("core")
	if err != nil {
		return vpngate.Job{}, err
	}
	m.mu.Lock()
	upstream := m.settings.Upstream
	job.Message = "读取官方版本信息 " + version
	initial := *job
	m.mu.Unlock()
	go func() {
		client, err := m.coreHTTPClient(upstream)
		if err == nil {
			defer client.CloseIdleConnections()
			client.Timeout = 10 * time.Minute
			err = m.installCore(ctx, job, client, version)
		}
		m.finishJob(job, err)
		if err == nil {
			m.startAfterCoreReady()
		}
	}()
	return initial, nil
}

func (m *Manager) coreProgress(job *vpngate.Job, message string, total, completed int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job.Message, job.Total, job.Completed = message, total, completed
}

func (m *Manager) installCore(ctx context.Context, job *vpngate.Job, client *http.Client, version string) error {
	data, err := coreMetadata(ctx, client, coreReleasesURL+"/tags/"+url.PathEscape(version))
	if err != nil {
		return err
	}
	var release githubCoreRelease
	if err = json.Unmarshal(data, &release); err != nil {
		return fmt.Errorf("官方版本元数据无效：%w", err)
	}
	if release.Tag != version || release.Draft {
		return fmt.Errorf("官方发行版本与请求不一致")
	}
	assetName := "sing-box-" + strings.TrimPrefix(version, "v") + "-windows-amd64.zip"
	var address, digest string
	for _, asset := range release.Assets {
		if asset.Name == assetName {
			if address != "" {
				return fmt.Errorf("官方发行包名称重复")
			}
			address, digest = asset.URL, asset.Digest
			if asset.Size < 1 || asset.Size > coreArchiveLimit {
				return fmt.Errorf("官方发行包大小无效或超过限制")
			}
		}
	}
	if address == "" {
		return fmt.Errorf("官方版本缺少 Windows amd64 ZIP 发行包")
	}
	expected, err := hex.DecodeString(strings.TrimPrefix(digest, "sha256:"))
	if !strings.HasPrefix(digest, "sha256:") || err != nil || len(expected) != sha256.Size {
		return fmt.Errorf("官方发行包缺少有效的 SHA-256 摘要，已拒绝下载")
	}
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/SagerNet/sing-box/releases/download/"+version+"/"+assetName {
		return fmt.Errorf("发行包地址不属于官方 SagerNet/sing-box 版本")
	}
	root := filepath.Join(m.dir, "cores")
	if err = os.MkdirAll(root, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(root)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("受管理内核目录无效")
	}
	stage, err := os.MkdirTemp(root, ".staging-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	archive := filepath.Join(stage, "download.zip")
	m.coreProgress(job, "通过已保存的外部上游下载 "+version, 0, 0)
	if err = m.downloadCoreArchive(ctx, job, client, address, archive, expected); err != nil {
		return err
	}
	payload := filepath.Join(stage, "payload")
	if err = extractCoreArchive(ctx, archive, payload, strings.TrimSuffix(assetName, ".zip")); err != nil {
		return err
	}
	m.coreProgress(job, "验证内核版本和配置 "+version, 1, 0)
	if err = m.checkCoreCandidate(ctx, filepath.Join(payload, "sing-box.exe"), version, stage); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	selection := "cores/" + version + "-" + id() + "/sing-box.exe"
	target, err := ownedCorePath(m.dir, selection)
	if err != nil {
		return err
	}
	if err = os.Rename(payload, filepath.Dir(target)); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(filepath.Dir(target))
		}
	}()
	m.op.Lock()
	defer m.op.Unlock()
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = m.activateCore(ctx, selection); err != nil {
		return err
	}
	committed = true
	m.coreProgress(job, "已安装并选用 "+version, 1, 1)
	return nil
}

func (m *Manager) downloadCoreArchive(ctx context.Context, job *vpngate.Job, client *http.Client, address, target string, expected []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("通过外部上游下载内核失败：%w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("内核下载失败：HTTP %d", response.StatusCode)
	}
	if response.ContentLength > coreArchiveLimit {
		return fmt.Errorf("内核下载超过大小限制")
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	hash := sha256.New()
	writer := io.MultiWriter(f, hash)
	buffer := make([]byte, 64<<10)
	var size int64
	reader := io.LimitReader(response.Body, coreArchiveLimit+1)
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		n, readErr := reader.Read(buffer)
		if n > 0 {
			size += int64(n)
			if size > coreArchiveLimit {
				return fmt.Errorf("内核下载超过大小限制")
			}
			if _, err = writer.Write(buffer[:n]); err != nil {
				return err
			}
			total := int(response.ContentLength)
			if total < 0 {
				total = 0
			}
			m.coreProgress(job, "正在下载内核", total, int(size))
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), hex.EncodeToString(expected)) {
		return fmt.Errorf("内核 ZIP 的 SHA-256 摘要不匹配，未执行或选用下载文件")
	}
	if err = f.Sync(); err != nil {
		return err
	}
	return f.Close()
}

func extractCoreArchive(ctx context.Context, archive, target, prefix string) error {
	z, err := zip.OpenReader(archive)
	if err != nil {
		return fmt.Errorf("无效的内核 ZIP：%w", err)
	}
	defer z.Close()
	if len(z.File) > 64 {
		return fmt.Errorf("内核 ZIP 文件数量超过限制")
	}
	if err = os.MkdirAll(target, 0700); err != nil {
		return err
	}
	var total int64
	executable := false
	seen := map[string]bool{}
	for _, file := range z.File {
		if err = ctx.Err(); err != nil {
			return err
		}
		name := file.Name
		if strings.Contains(name, "\\") || strings.Contains(name, ":") || strings.HasPrefix(name, "/") || path.Clean(name) != strings.TrimSuffix(name, "/") || file.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("内核 ZIP 含不安全的文件路径")
		}
		if file.FileInfo().IsDir() {
			if name != prefix+"/" {
				return fmt.Errorf("内核 ZIP 目录结构无效")
			}
			continue
		}
		if !strings.HasPrefix(name, prefix+"/") || strings.Count(name, "/") != 1 {
			return fmt.Errorf("内核 ZIP 目录结构无效")
		}
		base := strings.TrimPrefix(name, prefix+"/")
		upper := strings.ToUpper(base)
		if base != "sing-box.exe" && base != "libcronet.dll" && upper != "LICENSE" && !strings.HasPrefix(upper, "LICENSE.") && upper != "COPYING" && !strings.HasPrefix(upper, "COPYING.") {
			return fmt.Errorf("内核 ZIP 包含未允许的文件：%s", base)
		}
		if seen[strings.ToLower(base)] {
			return fmt.Errorf("内核 ZIP 文件重复")
		}
		seen[strings.ToLower(base)] = true
		if file.UncompressedSize64 > uint64(coreExtractLimit) || total+int64(file.UncompressedSize64) > coreExtractLimit {
			return fmt.Errorf("内核 ZIP 解压大小超过限制")
		}
		total += int64(file.UncompressedSize64)
		reader, err := file.Open()
		if err != nil {
			return err
		}
		output, err := os.OpenFile(filepath.Join(target, base), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
		if err != nil {
			reader.Close()
			return err
		}
		_, copyErr := io.Copy(output, io.LimitReader(reader, int64(file.UncompressedSize64)+1))
		closeErr := output.Close()
		reader.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if base == "sing-box.exe" {
			executable = true
		}
	}
	if !executable {
		return fmt.Errorf("内核 ZIP 缺少 sing-box.exe")
	}
	return nil
}

func (m *Manager) checkCoreCandidate(ctx context.Context, executable, requested, dir string) error {
	version, err := validateCore(ctx, executable)
	if err != nil {
		return err
	}
	if "v"+version != requested {
		return fmt.Errorf("下载内核报告版本 %s，与请求 %s 不一致", version, requested)
	}
	m.mu.Lock()
	settings := m.settings
	m.mu.Unlock()
	if settings.Upstream.Port == 0 {
		settings.Upstream.Port = 1
	}
	settings.TUNEnabled, settings.SystemProxyEnabled = false, false
	settings.CorePath = executable
	config, err := vpngate.BuildConfig(settings, nil, vpngate.ConfigOptions{Production: true, MixedPort: settings.MixedPort, CNRulesPath: filepath.Join(m.dir, "rules", "cn.srs"), GFWRulesPath: filepath.Join(m.dir, "rules", "gfw.srs")})
	if err != nil {
		return err
	}
	configPath := filepath.Join(dir, "check.json")
	if err = atomicWrite(configPath, config); err != nil {
		return err
	}
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(checkCtx, executable, "check", "-c", configPath, "-D", dir)
	hideCommand(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("下载内核配置校验失败：%w %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (m *Manager) activateCore(ctx context.Context, selection string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.closed || m.repairing || m.stopping || m.runtimeBlocksCoreChangeLocked() {
		return fmt.Errorf("软件状态已变化，未切换内核")
	}
	executable, err := ownedCorePath(m.dir, selection)
	if err != nil {
		return err
	}
	previousSettings, previousPath, previousSelection := m.settings, m.corePath, m.selectedCore
	previousNodes := m.pool.Snapshot()
	m.settings.CorePath, m.corePath, m.selectedCore = executable, executable, selection
	for _, node := range previousNodes {
		m.pool.UpdateProbe(vpngate.ProbeResult{NodeID: node.ID, Status: "untested"})
	}
	if err = m.persistLocked(); err != nil {
		m.settings, m.corePath, m.selectedCore = previousSettings, previousPath, previousSelection
		m.pool = vpngate.NewPool(previousNodes)
		return fmt.Errorf("保存内核选择失败，保留原内核：%w", err)
	}
	return nil
}

func (m *Manager) useCore(parent context.Context, rawVersion string) (vpngate.State, error) {
	if !m.desktop {
		return vpngate.State{}, fmt.Errorf("内核切换仅适用于独立桌面模式")
	}
	version, err := canonicalCoreVersion(rawVersion)
	if err != nil {
		return vpngate.State{}, err
	}
	m.op.Lock()
	defer m.op.Unlock()
	ctx, job, err := m.beginJob("core")
	if err != nil {
		return vpngate.State{}, err
	}
	defer func() {
		m.finishJob(job, err)
		if err == nil {
			go m.startAfterCoreReady()
		}
	}()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(parent, cancel)
	defer stop()
	m.mu.Lock()
	installed := m.installedCoresLocked()
	m.mu.Unlock()
	var candidate string
	for _, core := range installed {
		if core.Version == version {
			candidate = core.Path
			break
		}
	}
	if candidate == "" {
		err = fmt.Errorf("尚未下载 %s，请先安装该版本", version)
		return vpngate.State{}, err
	}
	stage, e := os.MkdirTemp(filepath.Join(m.dir, "cores"), ".check-")
	if e != nil {
		err = e
		return vpngate.State{}, err
	}
	defer os.RemoveAll(stage)
	if err = m.checkCoreCandidate(ctx, candidate, version, stage); err != nil {
		return vpngate.State{}, err
	}
	rel, e := filepath.Rel(m.dir, candidate)
	if e != nil {
		err = e
		return vpngate.State{}, err
	}
	if err = m.activateCore(ctx, filepath.ToSlash(rel)); err != nil {
		return vpngate.State{}, err
	}
	return m.State(), nil
}
