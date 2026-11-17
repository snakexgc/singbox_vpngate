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
	SID         string                   `json:"sid"`
	Values      map[string]registryValue `json:"values"`
	OwnedServer string                   `json:"ownedServer"`
}

const internetSettings = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

func desktopElevated() bool { return windows.GetCurrentProcessToken().IsElevated() }

func proxyRegistry(desktop bool) (registry.Key, string, error) {
	sid := ""
	if desktop {
		user, err := windows.GetCurrentProcessToken().GetTokenUser()
		if err != nil {
			return 0, "", fmt.Errorf("无法读取当前 GUI 用户 SID：%w", err)
		}
		sid = user.User.Sid.String()
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
	m.proxySnapshot.OwnedServer = "127.0.0.1:" + strconv.Itoa(port)
	data, err := json.Marshal(m.proxySnapshot)
	if err != nil {
		return err
	}
	if err = atomicWrite(filepath.Join(m.dir, "proxy-snapshot.json"), data); err != nil {
		return err
	}
	if err = m.persistLocked(); err != nil {
		return err
	}
	if err = k.SetStringValue("ProxyServer", "127.0.0.1:"+strconv.Itoa(port)); err != nil {
		return err
	}
	if err = k.SetStringValue("ProxyOverride", "localhost;127.*;[::1]"); err != nil {
		return err
	}
	if err = k.SetStringValue("AutoConfigURL", ""); err != nil {
		return err
	}
	if err = k.SetDWordValue("AutoDetect", 0); err != nil {
		return err
	}
	if err = k.SetDWordValue("ProxyEnable", 1); err != nil {
		return err
	}
	notifyProxy()
	return nil
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
	changed, err := restoreProxyValues(snapshot, current, m.settings.MixedPort, func(name string, v registryValue) error {
		if !v.Exists {
			e := k.DeleteValue(name)
			if e == registry.ErrNotExist {
				return nil
			}
			return e
		}
		return setRegistryRaw(k, name, v)
	})
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

func restoreProxyValues(snapshot *systemProxySnapshot, current string, fallbackPort int, write func(string, registryValue) error) (bool, error) {
	owned := snapshot.OwnedServer
	if owned == "" {
		owned = "127.0.0.1:" + strconv.Itoa(fallbackPort)
	}
	if current != owned {
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
	if _, exists := snapshot.Values["ProxyServer"]; exists {
		names = append(names, "ProxyServer")
	}
	for _, name := range names {
		if err := write(name, snapshot.Values[name]); err != nil {
			return false, err
		}
	}
	return true, nil
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
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		return nil, nil
	}
	conns, err := gnet.ConnectionsWithContext(ctx, "tcp")
	if err != nil {
		return nil, fmt.Errorf("无法识别外部 mixed 进程，请在设置填写进程路径：%w", err)
	}
	paths := []string{}
	for _, c := range conns {
		if int(c.Laddr.Port) != port || c.Pid <= 0 || c.Status != "LISTEN" {
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
		return nil, fmt.Errorf("未找到外部 mixed 的监听进程，请先启动上游代理，或手动填写其进程路径")
	}
	return paths, nil
}
