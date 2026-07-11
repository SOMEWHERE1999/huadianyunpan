//go:build windows

// Package service provides Windows service management using the SCM API.
// Business code must not depend on this package directly; use the
// RunFunc callback to inject daemon logic.
package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"
)

const (
	serviceWin32OwnProcess = 0x00000010
	serviceDemandStart     = 0x00000003
	serviceErrorNormal     = 0x00000001
	serviceAllAccess       = 0xF003F
	scManagerAllAccess     = 0xF003F

	serviceControlStop        = 0x00000001
	serviceControlInterrogate = 0x00000004

	serviceRunning     = 0x00000004
	serviceStopPending = 0x00000003
	serviceStopped     = 0x00000001
)

var (
	advapi32 = syscall.NewLazyDLL("advapi32.dll")

	procOpenSCManagerW                = advapi32.NewProc("OpenSCManagerW")
	procCreateServiceW                = advapi32.NewProc("CreateServiceW")
	procOpenServiceW                  = advapi32.NewProc("OpenServiceW")
	procDeleteService                 = advapi32.NewProc("DeleteService")
	procStartServiceW                 = advapi32.NewProc("StartServiceW")
	procControlService                = advapi32.NewProc("ControlService")
	procCloseServiceHandle            = advapi32.NewProc("CloseServiceHandle")
	procStartServiceCtrlDispatcherW   = advapi32.NewProc("StartServiceCtrlDispatcherW")
	procRegisterServiceCtrlHandlerExW = advapi32.NewProc("RegisterServiceCtrlHandlerExW")
	procSetServiceStatus              = advapi32.NewProc("SetServiceStatus")
)

type RunFunc func(ctx context.Context) error

type serviceStatus struct {
	ServiceType             uint32
	CurrentState            uint32
	ControlsAccepted        uint32
	Win32ExitCode           uint32
	ServiceSpecificExitCode uint32
	CheckPoint              uint32
	WaitHint                uint32
}

// Install creates a Windows service.
func Install(name, displayName, exePath string) error {
	if exePath == "" {
		var err error
		exePath, err = os.Executable()
		if err != nil {
			return fmt.Errorf("get executable: %w", err)
		}
		exePath, _ = filepath.Abs(exePath)
	}

	scmH, err := openSCManager()
	if err != nil {
		return err
	}
	defer closeHandle(scmH)

	namePtr, _ := syscall.UTF16PtrFromString(name)
	dispPtr, _ := syscall.UTF16PtrFromString(displayName)
	exePtr, _ := syscall.UTF16PtrFromString(exePath)

	r1, _, e1 := procCreateServiceW.Call(
		uintptr(scmH),
		uintptr(unsafe.Pointer(namePtr)),
		uintptr(unsafe.Pointer(dispPtr)),
		uintptr(serviceAllAccess),
		uintptr(serviceWin32OwnProcess),
		uintptr(serviceDemandStart),
		uintptr(serviceErrorNormal),
		uintptr(unsafe.Pointer(exePtr)),
		0, 0, 0, 0, 0,
	)
	if r1 == 0 {
		return fmt.Errorf("CreateService: %w", e1)
	}
	return nil
}

// Uninstall removes a Windows service.
func Uninstall(name string) error {
	scmH, err := openSCManager()
	if err != nil {
		return err
	}
	defer closeHandle(scmH)

	svcH, err := openService(scmH, name, serviceAllAccess)
	if err != nil {
		return err
	}
	defer closeHandle(svcH)

	r1, _, e1 := procDeleteService.Call(uintptr(svcH))
	if r1 == 0 {
		return fmt.Errorf("DeleteService: %w", e1)
	}
	return nil
}

// Start starts a Windows service.
func Start(name string) error {
	scmH, err := openSCManager()
	if err != nil {
		return err
	}
	defer closeHandle(scmH)

	svcH, err := openService(scmH, name, serviceAllAccess)
	if err != nil {
		return err
	}
	defer closeHandle(svcH)

	r1, _, e1 := procStartServiceW.Call(uintptr(svcH), 0, 0)
	if r1 == 0 {
		return fmt.Errorf("StartService: %w", e1)
	}
	return nil
}

// Stop stops a Windows service.
func Stop(name string) error {
	scmH, err := openSCManager()
	if err != nil {
		return err
	}
	defer closeHandle(scmH)

	svcH, err := openService(scmH, name, serviceAllAccess)
	if err != nil {
		return err
	}
	defer closeHandle(svcH)

	var status serviceStatus
	r1, _, e1 := procControlService.Call(
		uintptr(svcH),
		uintptr(serviceControlStop),
		uintptr(unsafe.Pointer(&status)),
	)
	if r1 == 0 {
		return fmt.Errorf("ControlService: %w", e1)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Service dispatcher
// ---------------------------------------------------------------------------

var (
	svcStopCh = make(chan struct{})
	svcRunFn  RunFunc
	svcName   string
)

// Run runs the daemon as a Windows service, or falls back to console mode
// if not started by the SCM.
func Run(name string, fn RunFunc) error {
	svcName = name
	svcRunFn = fn

	namePtr, _ := syscall.UTF16PtrFromString(name)
	type serviceTableEntry struct {
		name    *uint16
		handler uintptr
	}
	var table [2]serviceTableEntry
	table[0].name = namePtr
	table[0].handler = syscall.NewCallback(serviceMain)

	r1, _, _ := procStartServiceCtrlDispatcherW.Call(
		uintptr(unsafe.Pointer(&table[0])),
	)
	if r1 == 0 {
		// Not started by SCM — run in console mode.
		return fn(context.Background())
	}
	return nil
}

func serviceMain(argc uint32, argv **uint16) uintptr {
	hR1, _, _ := procRegisterServiceCtrlHandlerExW.Call(
		0,
		syscall.NewCallback(serviceCtrlHandler),
		0,
	)
	handle := syscall.Handle(hR1)

	reportStatus(handle, serviceRunning, 0)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		if svcRunFn != nil {
			svcRunFn(ctx)
		}
		cancel()
	}()
	defer cancel()

	<-svcStopCh
	reportStatus(handle, serviceStopPending, 3000)
	reportStatus(handle, serviceStopped, 0)
	return 0
}

func serviceCtrlHandler(ctrl uint32, evtype uint32, evdata unsafe.Pointer, ctx unsafe.Pointer) uintptr {
	if ctrl == serviceControlStop {
		select {
		case <-svcStopCh:
		default:
			close(svcStopCh)
		}
		return 0
	}
	if ctrl == serviceControlInterrogate {
		return 0
	}
	return 0
}

func reportStatus(handle syscall.Handle, state uint32, waitHint uint32) {
	status := serviceStatus{
		ServiceType:      serviceWin32OwnProcess,
		CurrentState:     state,
		ControlsAccepted: serviceControlStop | serviceControlInterrogate,
		WaitHint:         waitHint,
	}
	procSetServiceStatus.Call(uintptr(handle), uintptr(unsafe.Pointer(&status)))
}

// ---------------------------------------------------------------------------

func openSCManager() (syscall.Handle, error) {
	r1, _, e1 := procOpenSCManagerW.Call(0, 0, uintptr(scManagerAllAccess))
	h := syscall.Handle(r1)
	if h == 0 {
		return 0, fmt.Errorf("OpenSCManager: %w", e1)
	}
	return h, nil
}

func openService(scmH syscall.Handle, name string, access uint32) (syscall.Handle, error) {
	namePtr, _ := syscall.UTF16PtrFromString(name)
	r1, _, e1 := procOpenServiceW.Call(uintptr(scmH), uintptr(unsafe.Pointer(namePtr)), uintptr(access))
	h := syscall.Handle(r1)
	if h == 0 {
		return 0, fmt.Errorf("OpenService: %w", e1)
	}
	return h, nil
}

func closeHandle(h syscall.Handle) {
	procCloseServiceHandle.Call(uintptr(h))
}

var (
	_ = errors.New
	_ = time.Now
)
