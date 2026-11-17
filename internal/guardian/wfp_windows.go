//go:build windows && (amd64 || arm64)

package guardian

// ABI declarations below follow Windows SDK fwpmtypes.h/fwptypes.h. They are
// intentionally pointer-size aware and verified by layout tests on Windows.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var fwpuclnt = windows.NewLazySystemDLL("fwpuclnt.dll")
var iphlpapi = windows.NewLazySystemDLL("iphlpapi.dll")
var providerKey = guid("638a6439-17db-491e-8935-185bd94662d7")
var sublayerKey = guid("d999caf0-3ba0-47b8-a87e-e29c31b39355")
var ownership = []byte("SingBoxVPNGate/WFP/v1")
var connectLayers = []windows.GUID{guid("c38d57d1-05a7-4c33-904f-7fbceee60e82"), guid("4a72393b-319f-44bc-84c3-ba54dcb3b6b4")}
var receiveLayers = []windows.GUID{guid("e1cd9fe7-f4b5-4273-96c0-592e487b8650"), guid("a3b42c97-9f04-4672-b87e-cee9c483257f")}
var condApp = guid("d78e1e87-8644-4ea5-9437-d809ecefc971")
var condInterface = guid("4cd62a49-59c3-4969-b7f3-bda5d32890a4")
var condNextHop = guid("93ae8f5b-7f6f-4719-98c8-14e97429ef04")
var condRemote = guid("b235ae9a-1d64-49b8-a44c-5ff3d9095045")
var condProtocol = guid("3971ef2b-623e-4f9a-8cb1-6e79b806b9a7")
var condLocalPort = guid("0c1ba1af-5765-453f-af22-a8f791ac775b")
var condRemotePort = guid("c35a604d-d22b-4e1a-91b4-68f674ee674b")
var condFlags = guid("632ce23b-5167-435c-86d7-e903684aa80c")

func guid(s string) windows.GUID {
	v, e := windows.GUIDFromString("{" + s + "}")
	if e != nil {
		panic(e)
	}
	return v
}

type wfpBlob struct {
	Size uint32
	Data *byte
}
type wfpDisplay struct{ Name, Description *uint16 }
type wfpValue struct {
	Type  uint32
	Value uintptr
}
type wfpCondition struct {
	Key   windows.GUID
	Match uint32
	Value wfpValue
}
type wfpAction struct {
	Type uint32
	Key  windows.GUID
}
type wfpFilter struct {
	Key             windows.GUID
	Display         wfpDisplay
	Flags           uint32
	Provider        *windows.GUID
	Data            wfpBlob
	Layer, SubLayer windows.GUID
	Weight          wfpValue
	NumConditions   uint32
	Conditions      *wfpCondition
	Action          wfpAction
	Context         [2]uint64 // union{UINT64,GUID} has 8-byte alignment
	Reserved        *windows.GUID
	ID              uint64
	EffectiveWeight wfpValue
}
type wfpProvider struct {
	Key         windows.GUID
	Display     wfpDisplay
	Flags       uint32
	Data        wfpBlob
	ServiceName *uint16
}
type wfpSublayer struct {
	Key      windows.GUID
	Display  wfpDisplay
	Flags    uint32
	Provider *windows.GUID
	Data     wfpBlob
	Weight   uint16
}
type wfpEnumTemplate struct {
	Provider        *windows.GUID
	Layer           windows.GUID
	EnumType, Flags uint32
	Context         uintptr
	NumConditions   uint32
	Conditions      *wfpCondition
	ActionMask      uint32
	Callout         *windows.GUID
}
type wfpV4Mask struct{ Addr, Mask uint32 }
type wfpV6Mask struct {
	Addr   [16]byte
	Prefix uint8
}

func wfpCall(name string, args ...any) error {
	r := wfpCode(name, args...)
	if r != 0 {
		return fmt.Errorf("%s: WFP 0x%08x", name, uint32(r))
	}
	return nil
}
func wfpCode(name string, args ...any) uint32 {
	// Keep Go pointer arguments typed until the actual syscall, forcing them to
	// escape and pinning them across the call. Do not hide pointer->uintptr
	// conversions behind helper functions with uintptr return values.
	var pinned runtime.Pinner
	defer pinned.Unpin()
	native := make([]uintptr, len(args))
	for i, arg := range args {
		switch v := arg.(type) {
		case unsafe.Pointer:
			if v != nil {
				pinned.Pin(v)
			}
			native[i] = uintptr(v)
		case uintptr:
			native[i] = v
		case int:
			native[i] = uintptr(v)
		case uint32:
			native[i] = uintptr(v)
		default:
			panic(fmt.Sprintf("invalid WFP syscall argument %T", arg))
		}
	}
	r, _, _ := fwpuclnt.NewProc(name).Call(native...)
	runtime.KeepAlive(args)
	return uint32(r)
}
func utf(s string) *uint16 { return windows.StringToUTF16Ptr(s) }
func blob(b []byte) wfpBlob {
	if len(b) == 0 {
		return wfpBlob{}
	}
	return wfpBlob{uint32(len(b)), &b[0]}
}
func filterKey(name string) windows.GUID {
	sum := sha256.Sum256([]byte(ServiceName + "/" + name))
	var k windows.GUID
	copy(unsafe.Slice((*byte)(unsafe.Pointer(&k)), 16), sum[:16])
	return k
}
func ptr[T any](v *T) unsafe.Pointer { return unsafe.Pointer(v) }

type wfpArena struct{ pin runtime.Pinner }

func (a *wfpArena) pointer(p unsafe.Pointer) uintptr { a.pin.Pin(p); return uintptr(p) }
func (a *wfpArena) uint64(v uint64) uintptr {
	p := new(uint64)
	*p = v
	return a.pointer(unsafe.Pointer(p))
}

type windowsBackend struct{}

func newBackend() backend                  { return windowsBackend{} }
func (windowsBackend) servicePath() string { return servicePath() }
func (windowsBackend) validate(p Policy) error {
	if !IsService() {
		return errors.New("protection can only be armed by the installed Windows service")
	}
	if !samePath(p.CorePath, ManagedCorePath()) {
		return errors.New("core must be the protected managed installation")
	}
	for _, path := range []string{p.CorePath, servicePath()} {
		if err := verifyProtectedExecutable(path); err != nil {
			return err
		}
	}
	if err := verifyCoreDigest(p.CorePath); err != nil {
		return err
	}
	for _, path := range p.UpstreamProcessPaths {
		if err := verifyUpstreamExecutable(path); err != nil {
			return err
		}
	}
	return nil
}
func (windowsBackend) interfaceLUID(name string) (uint64, error) {
	var luid uint64
	r, _, _ := iphlpapi.NewProc("ConvertInterfaceAliasToLuid").Call(uintptr(unsafe.Pointer(utf(name))), uintptr(unsafe.Pointer(&luid)))
	if r == uintptr(windows.ERROR_INVALID_PARAMETER) || r == uintptr(windows.ERROR_NOT_FOUND) || r == uintptr(windows.ERROR_FILE_NOT_FOUND) {
		return 0, nil
	}
	if r != 0 {
		return 0, fmt.Errorf("resolve managed TUN: Windows error %d", r)
	}
	return luid, nil
}
func openWFP() (windows.Handle, error) {
	var h windows.Handle
	err := wfpCall("FwpmEngineOpen0", 0, 10, 0, 0, ptr(&h))
	return h, err
}                               // RPC_C_AUTHN_WINNT; NULL session: never dynamic.
func closeWFP(h windows.Handle) { _ = wfpCall("FwpmEngineClose0", uintptr(h)) }

func owned(f *wfpFilter) bool {
	return f != nil && f.Provider != nil && *f.Provider == providerKey && f.SubLayer == sublayerKey && f.Data.Size == uint32(len(ownership)) && f.Data.Data != nil && bytes.Equal(unsafe.Slice(f.Data.Data, int(f.Data.Size)), ownership)
}
func ownedFilters(h windows.Handle) ([]wfpFilter, error) {
	var result []wfpFilter
	for _, layer := range append(append([]windows.GUID(nil), connectLayers...), receiveLayers...) {
		fs, err := ownedLayerFilters(h, layer)
		if err != nil {
			return nil, err
		}
		result = append(result, fs...)
	}
	return result, nil
}
func ownedLayerFilters(h windows.Handle, layer windows.GUID) ([]wfpFilter, error) {
	// An actionMask of zero matches no actions. Explicitly include every action
	// and disabled owned filter so recovery cannot leave stale blocking entries.
	t := wfpEnumTemplate{Provider: &providerKey, Layer: layer, EnumType: 0, Flags: 0x10, ActionMask: 0xffffffff}
	var eh windows.Handle
	if code := wfpCode("FwpmFilterCreateEnumHandle0", uintptr(h), ptr(&t), ptr(&eh)); code != 0 {
		if code == 0x80320005 {
			return nil, nil
		}
		return nil, fmt.Errorf("enumerate owned WFP filters: 0x%x", code)
	}
	defer wfpCall("FwpmFilterDestroyEnumHandle0", uintptr(h), uintptr(eh))
	var result []wfpFilter
	for {
		var entries **wfpFilter
		var count uint32
		err := wfpCall("FwpmFilterEnum0", uintptr(h), uintptr(eh), 256, ptr(&entries), ptr(&count))
		if err != nil {
			return nil, err
		}
		if entries != nil {
			for _, f := range unsafe.Slice(entries, int(count)) {
				if owned(f) { // Deep-copy ownership data before freeing the enumeration page.
					c := *f
					c.Provider = &providerKey
					c.Data = blob(ownership)
					result = append(result, c)
				}
			}
			_ = wfpCall("FwpmFreeMemory0", ptr(&entries))
		}
		if count < 256 {
			break
		}
	}
	return result, nil
}

func ensureObjects(h windows.Handle) error {
	sd, err := windows.SecurityDescriptorFromString("O:SYG:SYD:P(A;;GA;;;SY)(A;;GA;;;BA)")
	if err != nil {
		return err
	}
	p := wfpProvider{Key: providerKey, Display: wfpDisplay{Name: utf("SingBox VPNGate protection")}, Flags: 1, Data: blob(ownership), ServiceName: utf(ServiceName)}
	// BFE re-enables persistent provider filters at startup for an automatic
	// service. This association depends on SCM start type, not whether the service
	// process is currently alive; service/core/GUI crashes retain the policy.
	if c := wfpCode("FwpmProviderAdd0", uintptr(h), ptr(&p), ptr(sd)); c != 0 && c != 0x80320009 {
		return fmt.Errorf("add WFP provider: 0x%x", c)
	}
	s := wfpSublayer{Key: sublayerKey, Display: wfpDisplay{Name: utf("SingBox VPNGate fail closed")}, Flags: 1, Provider: &providerKey, Data: blob(ownership), Weight: 0xffff}
	if c := wfpCode("FwpmSubLayerAdd0", uintptr(h), ptr(&s), ptr(sd)); c != 0 && c != 0x80320009 {
		return fmt.Errorf("add WFP sublayer: 0x%x", c)
	}
	runtime.KeepAlive(sd)
	return nil
}

func (windowsBackend) apply(ctx context.Context, rules []rule) error {
	h, err := openWFP()
	if err != nil {
		return err
	}
	defer closeWFP(h)
	if err = wfpCall("FwpmTransactionBegin0", uintptr(h), 0); err != nil {
		return err
	}
	defer wfpCall("FwpmTransactionAbort0", uintptr(h))
	if err = ensureObjects(h); err != nil {
		return err
	}
	old, err := ownedFilters(h)
	if err != nil {
		return err
	}
	for _, f := range old {
		if err = wfpCall("FwpmFilterDeleteByKey0", uintptr(h), ptr(&f.Key)); err != nil {
			return err
		}
	}
	for _, r := range rules {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = addRule(h, r); err != nil {
			return fmt.Errorf("%s: %w", r.Name, err)
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return wfpCall("FwpmTransactionCommit0", uintptr(h))
}

func addRule(h windows.Handle, r rule) error {
	var arena wfpArena
	defer arena.pin.Unpin()
	i := 0
	if r.V6 {
		i = 1
	}
	layer := connectLayers[i]
	if r.Inbound {
		layer = receiveLayers[i]
	}
	weight := uint64(100)
	action := uint32(0x1001)
	if !r.Block {
		weight = 10000
		action = 0x1002
	}
	f := wfpFilter{Key: filterKey(r.Name), Display: wfpDisplay{Name: utf("VPNGate " + r.Name)}, Flags: 1, Provider: &providerKey, Data: blob(ownership), Layer: layer, SubLayer: sublayerKey, Weight: wfpValue{Type: 4, Value: arena.uint64(weight)}, Action: wfpAction{Type: action}}
	var conditions []wfpCondition
	var app *wfpBlob
	if strings.HasPrefix(r.Name, "dhcp") {
		r.App = systemPath("svchost.exe")
	}
	if r.App != "" {
		if err := wfpCall("FwpmGetAppIdFromFileName0", ptr(utf(r.App)), ptr(&app)); err != nil {
			return err
		}
		defer wfpCall("FwpmFreeMemory0", ptr(&app))
		conditions = append(conditions, wfpCondition{Key: condApp, Value: wfpValue{Type: 12, Value: uintptr(unsafe.Pointer(app))}})
	}
	if r.Loopback {
		conditions = append(conditions, wfpCondition{Key: condFlags, Match: 6, Value: wfpValue{Type: 3, Value: 1}})
	}
	if r.Interface != 0 {
		key := condNextHop
		if r.Inbound {
			key = condInterface
		}
		conditions = append(conditions, wfpCondition{Key: key, Value: wfpValue{Type: 4, Value: arena.uint64(r.Interface)}})
	}
	var v4 wfpV4Mask
	var v6 wfpV6Mask
	if r.Remote.IsValid() {
		if r.V6 {
			v6.Addr = r.Remote.Addr().As16()
			v6.Prefix = uint8(r.Remote.Bits())
			conditions = append(conditions, wfpCondition{Key: condRemote, Value: wfpValue{Type: 0x101, Value: arena.pointer(unsafe.Pointer(&v6))}})
		} else {
			b := r.Remote.Addr().As4()
			v4.Addr = binary.BigEndian.Uint32(b[:])
			v4.Mask = ^uint32(0) << (32 - r.Remote.Bits())
			conditions = append(conditions, wfpCondition{Key: condRemote, Value: wfpValue{Type: 0x100, Value: arena.pointer(unsafe.Pointer(&v4))}})
		}
	}
	if r.Protocol != 0 {
		conditions = append(conditions, wfpCondition{Key: condProtocol, Value: wfpValue{Type: 1, Value: uintptr(r.Protocol)}})
	}
	if r.LocalPort != 0 {
		conditions = append(conditions, wfpCondition{Key: condLocalPort, Value: wfpValue{Type: 2, Value: uintptr(r.LocalPort)}})
	}
	if r.RemotePort != 0 {
		conditions = append(conditions, wfpCondition{Key: condRemotePort, Value: wfpValue{Type: 2, Value: uintptr(r.RemotePort)}})
	}
	if len(conditions) > 0 {
		f.NumConditions = uint32(len(conditions))
		f.Conditions = &conditions[0]
	}
	// No CLEAR_ACTION_RIGHT: exceptions are soft permits, preserving all other
	// firewall policy. The low-weight deny is skipped only inside this sublayer.
	err := wfpCall("FwpmFilterAdd0", uintptr(h), ptr(&f), 0, 0)
	runtime.KeepAlive(conditions)
	runtime.KeepAlive(weight)
	runtime.KeepAlive(r)
	runtime.KeepAlive(v4)
	runtime.KeepAlive(v6)
	return err
}

func (windowsBackend) remove(ctx context.Context) error {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return errors.New("administrator permission required to remove protection")
	}
	h, err := openWFP()
	if err != nil {
		return err
	}
	defer closeWFP(h)
	if err = wfpCall("FwpmTransactionBegin0", uintptr(h), 0); err != nil {
		return err
	}
	defer wfpCall("FwpmTransactionAbort0", uintptr(h))
	fs, err := ownedFilters(h)
	if err != nil {
		return err
	}
	for _, f := range fs {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = wfpCall("FwpmFilterDeleteByKey0", uintptr(h), ptr(&f.Key)); err != nil {
			return err
		}
	}
	return wfpCall("FwpmTransactionCommit0", uintptr(h))
}

func (windowsBackend) status() ProtectionStatus {
	h, err := openWFP()
	if err != nil {
		return ProtectionStatus{Message: err.Error()}
	}
	defer closeWFP(h)
	fs, err := ownedFilters(h)
	if err != nil {
		return ProtectionStatus{Message: err.Error()}
	}
	blocks := map[windows.GUID]bool{}
	for _, f := range fs {
		if f.Action.Type == 0x1001 && f.Flags&1 != 0 && f.Flags&0x20 == 0 && f.NumConditions == 0 {
			blocks[f.Key] = true
		}
	}
	ok := true
	for _, v6 := range []bool{false, true} {
		for _, in := range []bool{false, true} {
			ok = ok && blocks[filterKey(fmt.Sprintf("v6=%t/in=%t/deny", v6, in))]
		}
	}
	s := ProtectionStatus{Protected: ok, FilterCount: len(fs)}
	if ok {
		s.Message = "Persistent IPv4/IPv6 fail-closed filters are active"
	} else {
		s.Message = "Protection is not armed"
	}
	return s
}
