//go:build windows

package guardian

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const registryPath = `SOFTWARE\SingBoxVPNGate`

// Official sing-box 1.14.4 Windows amd64 archive was verified against the
// release API SHA256, then this executable digest was calculated from it.
const managedCoreSHA256AMD64 = "8f30682ee198855440d2a7fe070d5d3653793b2d01bb8c85eea88f93bbe7c8b9"

func verifyCoreDigest(path string) error {
	if runtime.GOARCH != "amd64" {
		return errors.New("managed sing-box 1.14.4 protection currently supports Windows amd64 only; no approved digest for this architecture")
	}
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return e
	}
	if hex.EncodeToString(h.Sum(nil)) != managedCoreSHA256AMD64 {
		return errors.New("managed core does not match the approved sing-box 1.14.4 executable SHA256")
	}
	return nil
}

// Upstream apps are explicitly trusted by the installed owner and are never
// executed/elevated by this service. Their user-writable locations are allowed;
// resistance to hostile same-user executable replacement is outside this trust
// model. The service and core retain the stricter protected installation rule.
func verifyUpstreamExecutable(path string) error {
	if !filepath.IsAbs(path) || !strings.EqualFold(filepath.Ext(path), ".exe") {
		return errors.New("upstream process requires an absolute existing .exe path")
	}
	if samePath(path, servicePath()) || samePath(path, ManagedCorePath()) {
		return errors.New("upstream process cannot be the managed service or core")
	}
	st, e := os.Stat(path)
	if e != nil {
		return e
	}
	if !st.Mode().IsRegular() {
		return errors.New("upstream executable is not a regular file")
	}
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		a, e := windows.GetFileAttributes(utf(p))
		if e != nil {
			return e
		}
		if a&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return fmt.Errorf("upstream executable path contains a reparse point: %s", p)
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return nil
}

func knownFolder(id *windows.KNOWNFOLDERID) string {
	p, e := windows.KnownFolderPath(id, 0)
	if e != nil {
		return ""
	}
	return p
}
func installDir() string {
	return filepath.Join(knownFolder(windows.FOLDERID_ProgramFiles), ServiceName)
}
func DataDir() string         { return filepath.Join(knownFolder(windows.FOLDERID_ProgramData), ServiceName) }
func ManagedCorePath() string { return filepath.Join(installDir(), "core", "sing-box.exe") }
func servicePath() string     { return filepath.Join(installDir(), "vpngate-service.exe") }
func systemPath(file string) string {
	dir, e := windows.GetSystemDirectory()
	if e != nil {
		return ""
	}
	return filepath.Join(dir, file)
}
func samePath(a, b string) bool {
	return filepath.IsAbs(a) && filepath.IsAbs(b) && strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
func OwnerSID() string {
	k, e := registry.OpenKey(registry.LOCAL_MACHINE, registryPath, registry.QUERY_VALUE)
	if e != nil {
		return ""
	}
	defer k.Close()
	v, _, _ := k.GetStringValue("OwnerSID")
	return v
}
func currentSID() (string, error) {
	u, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return "", e
	}
	return u.User.Sid.String(), nil
}
func IsService() bool {
	yes, e := svc.IsWindowsService()
	if e != nil || !yes {
		return false
	}
	exe, e := os.Executable()
	return e == nil && samePath(exe, servicePath())
}

// verifyProtectedExecutable rejects a user-writable executable, reparse points,
// and ancestors whose ACL lets an unprivileged principal replace descendants.
func verifyProtectedExecutable(path string) error {
	if !filepath.IsAbs(path) || !strings.EqualFold(filepath.Ext(path), ".exe") {
		return fmt.Errorf("trusted executable must have an absolute .exe path: %q", path)
	}
	st, e := os.Stat(path)
	if e != nil {
		return e
	}
	if !st.Mode().IsRegular() {
		return errors.New("trusted executable is not a regular file")
	}
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		a, e := windows.GetFileAttributes(utf(p))
		if e != nil {
			return e
		}
		if a&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return fmt.Errorf("reparse points are not allowed in trusted executable paths: %s", p)
		}
		sd, e := windows.GetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
		if e != nil {
			return e
		}
		owner, _, e := sd.Owner()
		if e != nil {
			return e
		}
		if !privilegedSID(owner) {
			return fmt.Errorf("untrusted owner for protected path: %s", p)
		}
		acl, _, e := sd.DACL()
		if e != nil || acl == nil {
			return fmt.Errorf("missing protected ACL: %s", p)
		}
		for i := uint32(0); i < uint32(acl.AceCount); i++ {
			var ace *windows.ACCESS_ALLOWED_ACE
			if e = windows.GetAce(acl, i, &ace); e != nil {
				return e
			}
			if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
				continue
			}
			if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
				if ace.Header.AceType == 5 { // ACCESS_ALLOWED_OBJECT_ACE_TYPE
					return fmt.Errorf("unsupported object ACL on trusted path: %s", p)
				}
				continue
			}
			mask := uint32(windows.GENERIC_WRITE | windows.GENERIC_ALL | windows.WRITE_DAC | windows.WRITE_OWNER | windows.DELETE | 0x40) // FILE_DELETE_CHILD
			if samePath(p, path) || strings.EqualFold(p, filepath.Dir(path)) {
				mask |= windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.FILE_WRITE_EA | windows.FILE_WRITE_ATTRIBUTES
			}
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			if uint32(ace.Mask)&mask != 0 && !privilegedSID(sid) {
				return fmt.Errorf("unprivileged write permission on trusted path: %s", p)
			}
		}
		if next := filepath.Dir(p); next == p {
			break
		}
	}
	return nil
}
func privilegedSID(s *windows.SID) bool {
	if s == nil {
		return false
	}
	v := s.String()
	return v == "S-1-5-18" || v == "S-1-5-32-544" || v == "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464"
}

func secureDirectory(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("installation directory is not absolute")
	}
	if _, e := os.Stat(path); e == nil {
		sd, e := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
		if e != nil {
			return e
		}
		owner, _, e := sd.Owner()
		if e != nil {
			return e
		}
		if !privilegedSID(owner) {
			return fmt.Errorf("refusing pre-existing installation directory with an untrusted owner: %s", path)
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	if e := os.MkdirAll(path, 0700); e != nil {
		return e
	}
	// Reject a directory planted as a link by an unprivileged user before first install.
	for p := path; ; p = filepath.Dir(p) {
		a, e := windows.GetFileAttributes(utf(p))
		if e != nil {
			return e
		}
		if a&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return fmt.Errorf("installation path contains a reparse point: %s", p)
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	sddl := "O:BAG:SYD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"
	// The nonprivileged GUI must be able to inspect/launch the service EXE for
	// explicit UAC recovery. The core subdirectory and ProgramData remain private
	// to SYSTEM/Admin so the owner cannot launch the privileged core with an
	// arbitrary config under its physically permitted WFP application identity.
	if samePath(path, installDir()) {
		sddl += "(A;OICI;FRFX;;;BU)"
	}
	sd, e := windows.SecurityDescriptorFromString(sddl)
	if e != nil {
		return e
	}
	d, _, e := sd.DACL()
	if e != nil {
		return e
	}
	owner, _, e := sd.Owner()
	if e != nil {
		return e
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION, owner, nil, d, nil)
}
func copyExecutable(src, dst string) error {
	if samePath(src, dst) {
		return verifyProtectedExecutable(dst)
	}
	// Open the source before modifying the destination; never follow a destination link.
	in, e := os.Open(src)
	if e != nil {
		return e
	}
	defer in.Close()
	st, e := in.Stat()
	if e != nil || !st.Mode().IsRegular() {
		return errors.New("installer source is not a regular executable")
	}
	if a, e := windows.GetFileAttributes(utf(dst)); e == nil && a&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("installer destination is a reparse point")
	}
	// A fresh file inherits the protected directory ACL. Atomic replacement
	// avoids following a pre-existing hard link or retaining a stale file ACL.
	out, e := os.CreateTemp(filepath.Dir(dst), ".install-*.exe")
	if e != nil {
		return e
	}
	tmp := out.Name()
	defer os.Remove(tmp)
	_, e = io.Copy(out, in)
	if closeErr := out.Close(); e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	if e = verifyProtectedExecutable(tmp); e != nil {
		return e
	}
	if e = windows.MoveFileEx(utf(tmp), utf(dst), windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); e != nil {
		return e
	}
	return verifyProtectedExecutable(dst)
}

// Install is only called by the explicit elevated installer command. It does
// not arm/disarm WFP and never connects. The distribution supplies both EXEs.
func Install() error {
	sid, e := currentSID()
	if e != nil {
		return e
	}
	return InstallForUser(sid)
}
func InstallForUser(sid string) error {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return errors.New("explicit administrator installation required")
	}
	parsed, e := windows.StringToSid(sid)
	if e != nil || parsed == nil || !strings.HasPrefix(sid, "S-1-5-21-") {
		return errors.New("installation requires a concrete local/domain user SID")
	}
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	srcService := filepath.Join(filepath.Dir(exe), "vpngate-service.exe")
	if strings.EqualFold(filepath.Base(exe), "vpngate-service.exe") {
		srcService = exe
	}
	srcCore := filepath.Join(filepath.Dir(exe), "sing-box.exe")
	if _, e = os.Stat(srcCore); e != nil {
		srcCore = filepath.Join(filepath.Dir(exe), "core", "sing-box.exe")
	}
	if _, e = os.Stat(srcService); e != nil {
		return fmt.Errorf("distribution service executable missing: %w", e)
	}
	if _, e = os.Stat(srcCore); e != nil {
		return fmt.Errorf("distribution managed sing-box executable missing: %w", e)
	}
	if e = verifyCoreDigest(srcCore); e != nil {
		return e
	}
	m, e := mgr.Connect()
	if e != nil {
		return e
	}
	defer m.Disconnect()
	if s, e := m.OpenService(ServiceName); e == nil {
		s.Close()
		return errors.New("service already installed; use explicit recovery or uninstall before reinstalling")
	}
	for _, dir := range []string{installDir(), filepath.Dir(ManagedCorePath()), DataDir()} {
		if e = secureDirectory(dir); e != nil {
			return e
		}
	}
	if e = copyExecutable(srcService, servicePath()); e != nil {
		return e
	}
	if e = copyExecutable(srcCore, ManagedCorePath()); e != nil {
		return e
	}
	k, _, e := registry.CreateKey(registry.LOCAL_MACHINE, registryPath, registry.SET_VALUE)
	if e != nil {
		return e
	}
	e = k.SetStringValue("OwnerSID", sid)
	k.Close()
	if e != nil {
		return e
	}
	s, e := m.CreateService(ServiceName, servicePath(), mgr.Config{DisplayName: "SingBox VPNGate protection", Description: "Persistent fail-closed VPN protection and managed runtime", StartType: mgr.StartAutomatic, ServiceStartName: "LocalSystem", Dependencies: []string{"BFE"}}, "--service")
	if e != nil {
		return e
	}
	defer s.Close()
	if e = s.SetRecoveryActions([]mgr.RecoveryAction{{Type: mgr.ServiceRestart, Delay: time.Second}, {Type: mgr.ServiceRestart, Delay: 3 * time.Second}, {Type: mgr.ServiceRestart, Delay: 10 * time.Second}}, 86400); e != nil {
		return e
	}
	if e = s.SetRecoveryActionsOnNonCrashFailures(true); e != nil {
		return e
	}
	return s.Start()
}

func stopService(s *mgr.Service) error {
	status, e := s.Query()
	if e != nil {
		return e
	}
	if status.State == svc.Stopped {
		return nil
	}
	if _, e = s.Control(svc.Stop); e != nil && e != windows.ERROR_SERVICE_NOT_ACTIVE {
		return e
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		status, e = s.Query()
		if e != nil {
			return e
		}
		if status.State == svc.Stopped {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("service stop timed out; protection remains armed")
}

// Recover deliberately restores direct access: stop the runtime first, then
// remove only this provider's owned filters. It never starts a VPN afterward.
func Recover() error {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return errors.New("explicit administrator recovery required")
	}
	// Restore the owner's system proxy via the live runtime before SCM shutdown.
	// The command host also restores the durable snapshot for offline recovery.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	_, _ = Call(ctx, "stop", json.RawMessage(`{}`))
	cancel()
	m, e := mgr.Connect()
	if e != nil {
		return e
	}
	defer m.Disconnect()
	var installed *mgr.Service
	if s, e := m.OpenService(ServiceName); e == nil {
		defer s.Close()
		installed = s
		if e = stopService(s); e != nil {
			return e
		}
	}
	if e = NewProtection().Disarm(context.Background()); e != nil {
		return e
	}
	// Restart only the service: runtime starts stopped, never reconnecting.
	if installed != nil {
		return installed.Start()
	}
	return nil
}
func Uninstall() error {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return errors.New("explicit administrator uninstall required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	_, _ = Call(ctx, "stop", json.RawMessage(`{}`))
	cancel()
	m, e := mgr.Connect()
	if e != nil {
		return e
	}
	defer m.Disconnect()
	s, e := m.OpenService(ServiceName)
	if e == windows.ERROR_SERVICE_DOES_NOT_EXIST {
		return NewProtection().Disarm(context.Background())
	}
	if e != nil {
		return e
	}
	defer s.Close()
	if e = stopService(s); e != nil {
		return e
	}
	if e = NewProtection().Disarm(context.Background()); e != nil {
		return e
	}
	return s.Delete()
}
func ServiceStatus() ServiceInfo {
	info := ServiceInfo{RequiresAdmin: !windows.GetCurrentProcessToken().IsElevated()}
	s, e := openQueryService()
	if e != nil {
		info.Message = "Protection service is not installed"
		return info
	}
	defer s.Close()
	info.Installed = true
	st, e := s.Query()
	if e != nil {
		info.Message = e.Error()
		return info
	}
	info.Running = st.State == svc.Running
	if info.Running {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		data, e := Call(ctx, "service.status", nil)
		if e == nil {
			var v ServiceInfo
			if jsonUnmarshal(data, &v) == nil {
				info.Protected = v.Protected
			}
		}
	}
	if !info.Running {
		info.Message = "Protection service is stopped; persistent filters may still be active"
	} else {
		info.Message = "Protection service is running"
	}
	return info
}

// mgr.Connect/OpenService request ALL_ACCESS and cannot be used by the ordinary
// GUI. Query handles need only CONNECT and QUERY_STATUS.
func openQueryService() (*mgr.Service, error) {
	scm, e := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if e != nil {
		return nil, e
	}
	defer windows.CloseServiceHandle(scm)
	h, e := windows.OpenService(scm, utf(ServiceName), windows.SERVICE_QUERY_STATUS)
	if e != nil {
		return nil, e
	}
	return &mgr.Service{Name: ServiceName, Handle: h}, nil
}

func Elevate(action string) error {
	if action != "install" && action != "recover" && action != "uninstall" {
		return errors.New("invalid elevated action")
	}
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	target := servicePath()
	if action == "install" {
		target = filepath.Join(filepath.Dir(exe), "vpngate-service.exe")
	}
	if _, e = os.Stat(target); e != nil {
		return e
	}
	sid, e := currentSID()
	if e != nil {
		return e
	}
	args := "--" + action + " --owner-sid " + sid
	return windows.ShellExecute(0, utf("runas"), utf(target), utf(args), utf(filepath.Dir(target)), windows.SW_HIDE)
}
