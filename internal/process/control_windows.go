package process

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func supervise(ctx context.Context, cmd *exec.Cmd) (func() error, func() error, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return nil, nil, err
	}
	// No application instruction may run before association with the job:
	// assignment after a normal Start cannot capture already-created children.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_SUSPENDED}
	if strings.EqualFold(filepath.Base(cmd.Path), "cmd.exe") && len(cmd.Args) == 5 && cmd.Args[1] == "/D" && cmd.Args[2] == "/S" && cmd.Args[3] == "/C" {
		cmd.SysProcAttr.CmdLine = syscall.EscapeArg(cmd.Path) + ` /D /S /C "` + cmd.Args[4] + `"`
	}
	var attached atomic.Bool
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		if !attached.Load() {
			return cmd.Process.Kill()
		}
		return windows.TerminateJobObject(job, 1)
	}
	attach := func() error {
		handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
		if err != nil {
			return err
		}
		defer func() { _ = windows.CloseHandle(handle) }()
		if err := windows.AssignProcessToJobObject(job, handle); err != nil {
			return err
		}
		attached.Store(true)
		if err := ctx.Err(); err != nil {
			return err
		}
		return resumePrimaryThread(uint32(cmd.Process.Pid))
	}
	return attach, func() error { return windows.CloseHandle(job) }, nil
}

func resumePrimaryThread(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err := windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			return err
		}
		previous, resumeErr := windows.ResumeThread(thread)
		closeErr := windows.CloseHandle(thread)
		if resumeErr != nil {
			return resumeErr
		}
		if previous != 1 {
			return fmt.Errorf("unexpected primary-thread suspend count %d", previous)
		}
		return closeErr
	}
	return fmt.Errorf("suspended process primary thread was not found")
}

func processSignal(*os.ProcessState) string { return "" }

func processInterrupted(state *os.ProcessState) bool { return state != nil && state.ExitCode() == 130 }
