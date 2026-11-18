//go:build windows

package vpngateruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	gnet "github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"guiforcores/internal/guardian"
)

func hideCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}
func ownProcess(cmd *exec.Cmd) (func(), error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	defer windows.CloseHandle(process)
	if err = windows.AssignProcessToJobObject(job, process); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	return func() { windows.CloseHandle(job) }, nil
}
func replaceFile(from, to string) error {
	src, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	dst, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(src, dst, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

type registryValue struct {
	Exists bool   `json:"exists"`
	Type   uint32 `json:"type"`
	Data   []byte `json:"data"`
}
type systemProxySnapshot struct {
	SID             string                   `json:"sid"`
	Values          map[string]registryValue `json:"values"`
	OwnedServer     string                   `json:"ownedServer"`
	PendingServer   string                   `json:"pendingServer,omitempty"`
	ConnectionFlags *uint32                  `json:"connectionFlags,omitempty"`
}

const internetSettings = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

func desktopElevated() bool { return windows.GetCurrentProcessToken().IsElevated() }

func currentUserSID() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("无法读取当前 GUI 用户 SID：%w", err)
	}
	return user.User.Sid.String(), nil
}

func proxyRegistry(desktop bool) (registry.Key, string, error) {
	sid := ""
	if desktop {
		var err error
		sid, err = currentUserSID()
		if err != nil {
			return 0, "", err
		}
	} else {
		sid = guardian.OwnerSID()
	}
	if sid == "" {
		return 0, "", fmt.Errorf("保护服务尚未配置用户 SID")
	}
	k, err := registry.OpenKey(registry.USERS, sid+`\`+internetSettings, registry.QUERY_VALUE|registry.SET_VALUE)
	return k, sid, err
}
func (m *Manager) applySystemProxy(port int) error {
	k, sid, err := proxyRegistry(m.desktop)
	if err != nil {
		return err
	}
	defer k.Close()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.proxySnapshot == nil {
		snapshot := &systemProxySnapshot{SID: sid, Values: map[string]registryValue{}}
		for _, name := range []string{"ProxyEnable", "ProxyServer", "ProxyOverride", "AutoConfigURL", "AutoDetect"} {
			n, typ, e := k.GetValue(name, nil)
			if e == registry.ErrNotExist {
				snapshot.Values[name] = registryValue{}
				continue
			}
			if e != nil {
				return e
			}
			buf := make([]byte, n)
			_, _, e = k.GetValue(name, buf)
			if e != nil {
				return e
			}
			snapshot.Values[name] = registryValue{true, typ, buf}
		}
		m.proxySnapshot = snapshot
	}
	if err = captureProxyConnectionFlags(m.proxySnapshot, m.desktop, sid, func() (uint32, error) { return queryConnectionFlags(queryConnectionFlagsOption) }); err != nil {
		return err
	}
	current, _, err := k.GetStringValue("ProxyServer")
	if err != nil && err != registry.ErrNotExist {
		return err
	}
	err = applyProxySettings(m.proxySnapshot, current, "127.0.0.1:"+strconv.Itoa(port), func() error {
		data, err := json.Marshal(m.proxySnapshot)
		if err != nil {
			return err
		}
		if err = atomicWrite(filepath.Join(m.dir, "proxy-snapshot.json"), data); err != nil {
			return err
		}
		return m.persistLocked()
	}, k.SetStringValue, k.SetDWordValue, setConnectionFlags)
	if err != nil {
		return err
	}
	notifyProxy()
	return nil
}

func captureProxyConnectionFlags(snapshot *systemProxySnapshot, desktop bool, sid string, query func() (uint32, error)) error {
	// WinINet's per-connection flags, rather than an AutoDetect registry value,
	// control LAN PAC/WPAD. Upgrade older desktop snapshots before changing them.
	// A service must never call current-HKCU WinINet APIs on behalf of its owner.
	if !desktop {
		if snapshot.ConnectionFlags != nil {
			return fmt.Errorf("WinINet 连接选项需由原用户桌面进程设置")
		}
		return nil
	}
	if err := validateProxyFlagsOwner(snapshot.SID, sid); err != nil {
		return err
	}
	if snapshot.ConnectionFlags == nil {
		flags, err := query()
		if err != nil {
			return err
		}
		snapshot.ConnectionFlags = &flags
	}
	return nil
}

func applyProxySettings(snapshot *systemProxySnapshot, current, server string, persist func() error, writeString func(string, string) error, writeDWORD func(string, uint32) error, writeFlags func(uint32) error) error {
	// A crash or failed registry write may leave either endpoint installed. Keep
	// both identities until the new configuration is durably committed.
	if snapshot.PendingServer != "" && current == snapshot.PendingServer {
		snapshot.OwnedServer = current
	}
	snapshot.PendingServer = server
	if err := persist(); err != nil {
		return err
	}
	if err := writeString("ProxyServer", server); err != nil {
		return err
	}
	if err := writeString("ProxyOverride", "localhost;127.*;[::1]"); err != nil {
		return err
	}
	if err := writeString("AutoConfigURL", ""); err != nil {
		return err
	}
	if err := writeDWORD("AutoDetect", 0); err != nil {
		return err
	}
	if err := writeDWORD("ProxyEnable", 1); err != nil {
		return err
	}
	if snapshot.ConnectionFlags != nil {
		if writeFlags == nil {
			return fmt.Errorf("WinINet 连接选项写入不可用")
		}
		if err := writeFlags(3); err != nil { // PROXY_TYPE_DIRECT | PROXY_TYPE_PROXY
			return err
		}
	}
	snapshot.OwnedServer, snapshot.PendingServer = server, ""
	return persist()
}
func (m *Manager) restoreSystemProxy() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshot := m.proxySnapshot
	if snapshot == nil {
		return nil
	}
	k, err := registry.OpenKey(registry.USERS, snapshot.SID+`\`+internetSettings, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	// Preserve a proxy deliberately changed by another application while active.
	current, _, err := k.GetStringValue("ProxyServer")
	if err != nil && err != registry.ErrNotExist {
		return err
	}
	if snapshot.ConnectionFlags != nil && ownsProxyServer(snapshot, current, m.settings.MixedPort) {
		sid, err := currentUserSID()
		if err != nil {
			return err
		}
		if err = validateProxyFlagsOwner(snapshot.SID, sid); err != nil {
			return err
		}
	}
	changed, err := restoreProxyValues(snapshot, current, m.settings.MixedPort, func(name string, v registryValue) error {
		if !v.Exists {
			e := k.DeleteValue(name)
			if e == registry.ErrNotExist {
				return nil
			}
			return e
		}
		return setRegistryRaw(k, name, v)
	}, setConnectionFlags)
	if err != nil {
		return err
	}
	if changed {
		notifyProxy()
	}
	if err = os.Remove(filepath.Join(m.dir, "proxy-snapshot.json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	m.proxySnapshot = nil
	return nil
}

func restoreProxyValues(snapshot *systemProxySnapshot, current string, fallbackPort int, write func(string, registryValue) error, writeFlags func(uint32) error) (bool, error) {
	if !ownsProxyServer(snapshot, current, fallbackPort) {
		return false, nil
	}
	// Keep the owned endpoint until every other value is restored. Otherwise a
	// partial write failure would make the next retry mistake our restored
	// ProxyServer for another application's endpoint and discard the snapshot.
	names := make([]string, 0, len(snapshot.Values))
	for name := range snapshot.Values {
		if name != "ProxyServer" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if err := write(name, snapshot.Values[name]); err != nil {
			return false, err
		}
	}
	if snapshot.ConnectionFlags != nil {
		if writeFlags == nil {
			return false, fmt.Errorf("WinINet 连接选项恢复不可用")
		}
		if err := writeFlags(*snapshot.ConnectionFlags); err != nil {
			return false, err
		}
	}
	if value, exists := snapshot.Values["ProxyServer"]; exists {
		if err := write("ProxyServer", value); err != nil {
			return false, err
		}
	}
	return true, nil
}

func ownsProxyServer(snapshot *systemProxySnapshot, current string, fallbackPort int) bool {
	owned := snapshot.OwnedServer
	if owned == "" {
		owned = "127.0.0.1:" + strconv.Itoa(fallbackPort)
	}
	return current == owned || snapshot.PendingServer != "" && current == snapshot.PendingServer
}

func validateProxyFlagsOwner(savedSID, currentSID string) error {
	if savedSID == "" || savedSID != currentSID {
		return fmt.Errorf("WinINet 系统代理需在原用户账号下恢复；请以该用户重新启动 GUI 后修复")
	}
	return nil
}

// These structures follow WinSDK wininet.h. Value represents the eight-byte
// DWORD/LPWSTR/FILETIME union; its alignment matches the pointer-sized ABI.
type internetPerConnOption struct {
	Option uint32
	Value  uint64
}
type internetPerConnOptionList struct {
	Size        uint32
	Connection  *uint16
	Count       uint32
	OptionError uint32
	Options     *internetPerConnOption
}

func queryConnectionFlags(query func(uint32) (uint32, error)) (uint32, error) {
	flags, err := query(10) // INTERNET_PER_CONN_FLAGS_UI
	if err != nil {
		return query(1) // INTERNET_PER_CONN_FLAGS on older WinINet
	}
	return flags, nil
}

func queryConnectionFlagsOption(option uint32) (uint32, error) {
	return connectionFlagsOption(false, option, 0)
}

func setConnectionFlags(flags uint32) error {
	_, err := connectionFlagsOption(true, 1, flags) // restore/set FLAGS, never FLAGS_UI
	return err
}

func connectionFlagsOption(set bool, optionID, flags uint32) (uint32, error) {
	option := internetPerConnOption{Option: optionID, Value: uint64(flags)}
	list := internetPerConnOptionList{Count: 1, Options: &option} // NULL Connection is LAN
	list.Size = uint32(unsafe.Sizeof(list))
	size := list.Size
	name := "InternetQueryOptionW"
	var result uintptr
	var err error
	if set {
		name = "InternetSetOptionW"
		result, _, err = windows.NewLazySystemDLL("wininet.dll").NewProc(name).Call(0, 75, uintptr(unsafe.Pointer(&list)), uintptr(size))
	} else {
		result, _, err = windows.NewLazySystemDLL("wininet.dll").NewProc(name).Call(0, 75, uintptr(unsafe.Pointer(&list)), uintptr(unsafe.Pointer(&size)))
	}
	runtime.KeepAlive(&option)
	runtime.KeepAlive(&list)
	if result == 0 {
		if err == syscall.Errno(0) {
			err = windows.ERROR_GEN_FAILURE
		}
		return 0, fmt.Errorf("%s LAN connection flags: %w", name, err)
	}
	return uint32(option.Value), nil
}
func setRegistryRaw(k registry.Key, name string, v registryValue) error {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	var data *byte
	if len(v.Data) > 0 {
		data = &v.Data[0]
	}
	call := windows.NewLazySystemDLL("advapi32.dll").NewProc("RegSetValueExW")
	r, _, _ := call.Call(uintptr(k), uintptr(unsafe.Pointer(p)), 0, uintptr(v.Type), uintptr(unsafe.Pointer(data)), uintptr(len(v.Data)))
	if r != 0 {
		return syscall.Errno(r)
	}
	return nil
}
func notifyProxy() {
	p := windows.NewLazySystemDLL("wininet.dll").NewProc("InternetSetOptionW")
	p.Call(0, 39, 0, 0)
	p.Call(0, 37, 0, 0)
}

func resolveUpstreamOwner(ctx context.Context, host string, port int) ([]string, error) {
	ip := net.ParseIP(host)
	if strings.EqualFold(host, "localhost") {
		ip = net.IPv4(127, 0, 0, 1)
	}
	if ip == nil {
		return nil, nil
	}
	if !ip.IsLoopback() {
		addresses, err := net.InterfaceAddrs()
		if err != nil {
			return nil, fmt.Errorf("无法判断上游代理地址是否属于本机：%w", err)
		}
		if !localInterfaceAddress(ip, addresses) {
			return nil, nil
		}
	}
	conns, err := gnet.ConnectionsWithContext(ctx, "tcp")
	if err != nil {
		return nil, fmt.Errorf("无法识别本机上游代理的监听程序：%w", err)
	}
	paths := []string{}
	for _, c := range conns {
		if !matchesUpstreamListener(ip, port, c.Laddr.IP, c.Laddr.Port) || c.Pid <= 0 || c.Status != "LISTEN" {
			continue
		}
		p, e := process.NewProcessWithContext(ctx, c.Pid)
		if e != nil {
			continue
		}
		path, e := p.ExeWithContext(ctx)
		if e == nil && path != "" {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("未找到本机上游代理的监听程序，请启动代理并确认监听地址和端口")
	}
	return paths, nil
}

func localInterfaceAddress(ip net.IP, addresses []net.Addr) bool {
	for _, address := range addresses {
		if local, ok := address.(*net.IPNet); ok && local.IP.Equal(ip) {
			return true
		}
	}
	return false
}

func matchesUpstreamListener(upstream net.IP, port int, listenerHost string, listenerPort uint32) bool {
	listener := net.ParseIP(listenerHost)
	if int(listenerPort) != port || upstream == nil || listener == nil {
		return false
	}
	if listener.Equal(upstream) {
		return true
	}
	// A same-family wildcard accepts this address. IPv6 dual-stack behavior
	// cannot be established from the listening table alone; do not trust an
	// unrelated IPv6-only listener for an IPv4 upstream.
	return listener.IsUnspecified() && (listener.To4() != nil) == (upstream.To4() != nil)
}
