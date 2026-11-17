//go:build windows

package guardian

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

type pipeStream struct {
	h   windows.Handle
	ctx context.Context
}

func (p pipeStream) Read(b []byte) (int, error)  { return p.transfer(b, false) }
func (p pipeStream) Write(b []byte) (int, error) { return p.transfer(b, true) }
func (p pipeStream) transfer(b []byte, write bool) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	if e := p.ctx.Err(); e != nil {
		return 0, e
	}
	event, e := windows.CreateEvent(nil, 1, 0, nil)
	if e != nil {
		return 0, e
	}
	defer windows.CloseHandle(event)
	ov := windows.Overlapped{HEvent: event}
	var n uint32
	if write {
		e = windows.WriteFile(p.h, b, &n, &ov)
	} else {
		e = windows.ReadFile(p.h, b, &n, &ov)
	}
	if e == windows.ERROR_IO_PENDING {
		e = awaitIO(p.ctx, p.h, &ov)
		if e == nil {
			e = windows.GetOverlappedResult(p.h, &ov, &n, false)
		}
	}
	if e == windows.ERROR_BROKEN_PIPE {
		return int(n), io.EOF
	}
	return int(n), e
}
func awaitIO(ctx context.Context, h windows.Handle, ov *windows.Overlapped) error {
	for {
		v, e := windows.WaitForSingleObject(ov.HEvent, 50)
		if e != nil {
			return e
		}
		if v == windows.WAIT_OBJECT_0 {
			return nil
		}
		if e = ctx.Err(); e != nil {
			_ = windows.CancelIoEx(h, ov)
			var done uint32
			_ = windows.GetOverlappedResult(h, ov, &done, true)
			return e
		}
	}
}

func authenticateServer(h windows.Handle) error {
	var pid uint32
	if e := windows.GetNamedPipeServerProcessId(h, &pid); e != nil {
		return e
	}
	s, e := openQueryService()
	if e != nil {
		return ErrUnavailable
	}
	defer s.Close()
	st, e := s.Query()
	if e != nil {
		return e
	}
	if st.State != svc.Running || st.ProcessId != pid || pid == 0 {
		return errors.New("pipe server does not match installed running service")
	}
	p, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if e != nil {
		return e
	}
	defer windows.CloseHandle(p)
	var name [32768]uint16
	n := uint32(len(name))
	if e = windows.QueryFullProcessImageName(p, 0, &name[0], &n); e != nil {
		return e
	}
	if !samePath(windows.UTF16ToString(name[:n]), servicePath()) {
		return errors.New("unexpected protection service image")
	}
	return verifyProtectedExecutable(servicePath())
}

func Call(ctx context.Context, method string, payload json.RawMessage) (json.RawMessage, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	var h windows.Handle
	for {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		var e error
		h, e = windows.CreateFile(utf(PipeName), windows.GENERIC_READ|windows.FILE_WRITE_DATA, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED|windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IDENTIFICATION, 0)
		if e == nil {
			break
		}
		if e != windows.ERROR_PIPE_BUSY {
			return nil, fmt.Errorf("%w: %v", ErrUnavailable, e)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	defer windows.CloseHandle(h)
	if e := authenticateServer(h); e != nil {
		return nil, e
	}
	stream := pipeStream{h, ctx}
	if e := writeFrame(stream, request{Version: 1, Method: method, Payload: payload}); e != nil {
		return nil, e
	}
	res, e := receiveResponse(stream)
	if e != nil {
		return nil, e
	}
	if res.Error != "" {
		return nil, errors.New(res.Error)
	}
	return res.Data, nil
}

func pipeSD(owner string) (*windows.SECURITY_DESCRIPTOR, error) {
	sid, e := windows.StringToSid(owner)
	if e != nil || sid == nil || owner == "" {
		return nil, errors.New("installed owner SID is required for restricted IPC")
	}
	// Creator-owner is intentionally absent. The owner gets read/write only;
	// SYSTEM/Admin are the only principals allowed to create server instances.
	// FILE_GENERIC_WRITE would include FILE_CREATE_PIPE_INSTANCE. Grant the
	// concrete file rights with that bit removed, so the owner cannot impersonate
	// the server by creating another instance of this pipe.
	return windows.SecurityDescriptorFromString("O:SYG:SYD:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;0x12019b;;;" + sid.String() + ")")
}

func authenticateClient(h windows.Handle, owner string) error {
	var pid uint32
	if e := windows.GetNamedPipeClientProcessId(h, &pid); e != nil {
		return e
	}
	p, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if e != nil {
		return e
	}
	defer windows.CloseHandle(p)
	var token windows.Token
	if e = windows.OpenProcessToken(p, windows.TOKEN_QUERY, &token); e != nil {
		return e
	}
	defer token.Close()
	u, e := token.GetTokenUser()
	if e != nil {
		return e
	}
	if u.User.Sid.String() != owner && !u.User.Sid.IsWellKnown(windows.WinLocalSystemSid) && !token.IsElevated() {
		return errors.New("IPC caller is not the installed owner")
	}
	return nil
}

func Serve(ctx context.Context, h Handler) error {
	if !IsService() {
		return errors.New("pipe server must run inside the installed Windows service")
	}
	owner := OwnerSID()
	sd, e := pipeSD(owner)
	if e != nil {
		return e
	}
	attrs := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	var workers sync.WaitGroup
	defer workers.Wait()
	// Acquire before creating an instance, retaining native headroom below the
	// 32-instance limit. Saturation waits for a worker rather than killing SCM.
	slots := newConnectionSlots(16)
	// FIRST_PIPE_INSTANCE rejects an already occupied name at startup. Every
	// client also authenticates the SCM PID and protected executable identity.
	first := true
	for {
		if e = ctx.Err(); e != nil {
			return nil
		}
		if e = slots.acquire(ctx); e != nil {
			return nil
		}
		flags := uint32(windows.PIPE_ACCESS_DUPLEX | windows.FILE_FLAG_OVERLAPPED)
		if first {
			flags |= windows.FILE_FLAG_FIRST_PIPE_INSTANCE
		}
		pipe, e := windows.CreateNamedPipe(utf(PipeName), flags, windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS, 32, 64<<10, 64<<10, 1000, &attrs)
		if e != nil {
			slots.release()
			if e == windows.ERROR_PIPE_BUSY {
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(50 * time.Millisecond):
					continue
				}
			}
			return e
		}
		first = false
		event, e := windows.CreateEvent(nil, 1, 0, nil)
		if e != nil {
			windows.CloseHandle(pipe)
			slots.release()
			return e
		}
		ov := windows.Overlapped{HEvent: event}
		e = windows.ConnectNamedPipe(pipe, &ov)
		if e == windows.ERROR_IO_PENDING {
			e = awaitIO(ctx, pipe, &ov)
		} else if e == windows.ERROR_PIPE_CONNECTED {
			e = nil
		}
		windows.CloseHandle(event)
		if e != nil {
			windows.CloseHandle(pipe)
			slots.release()
			if ctx.Err() != nil {
				return nil
			}
			return e
		}
		if e = authenticateClient(pipe, owner); e != nil {
			windows.DisconnectNamedPipe(pipe)
			windows.CloseHandle(pipe)
			slots.release()
			continue
		}
		workers.Add(1)
		go func(pipe windows.Handle) {
			defer workers.Done()
			defer slots.release()
			defer windows.CloseHandle(pipe)
			defer windows.DisconnectNamedPipe(pipe)
			connCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			stream := pipeStream{pipe, connCtx}
			var req request
			if e := readFrame(stream, &req); e != nil {
				return
			}
			res := dispatch(connCtx, h, req)
			// A stalled/misbehaving client cannot retain a pipe indefinitely while
			// the server waits for proof the buffered response was fully consumed.
			deliveryCtx, deliveryCancel := context.WithTimeout(connCtx, 10*time.Second)
			defer deliveryCancel()
			_ = sendResponse(pipeStream{pipe, deliveryCtx}, res)
		}(pipe)
	}
}

type serviceHost struct{ handler Handler }

func (h serviceHost) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, h.handler) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case e := <-done:
			if e != nil {
				return true, 1
			}
			return false, 0
		case req := <-requests:
			switch req.Cmd {
			case svc.Interrogate:
				status <- req.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
				}
				return false, 0
			}
		}
	}
}
func RunService(h Handler) error {
	if !IsService() {
		return errors.New("service host must be launched by SCM from its protected installation")
	}
	return svc.Run(ServiceName, serviceHost{h})
}

// RestrictProcessTree puts this service and every subsequently created child in
// a kill-on-close job. Kernel handle cleanup kills orphaned cores on a crash;
// persistent WFP policy survives that cleanup.
func RestrictProcessTree() (func(), error) {
	job, e := windows.CreateJobObject(nil, nil)
	if e != nil {
		return nil, e
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, e = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); e != nil {
		windows.CloseHandle(job)
		return nil, e
	}
	if e = windows.AssignProcessToJobObject(job, windows.CurrentProcess()); e != nil {
		windows.CloseHandle(job)
		return nil, e
	}
	// The service job must remain open until process exit. Explicitly closing it
	// while this process belongs to it would terminate the service as well.
	return func() { _ = job; _ = os.Getpid() }, nil
}

func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
