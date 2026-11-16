//go:build windows

package cli

import (
	"context"
	"io"
	"os"
	"runtime"
	"time"

	"golang.org/x/sys/windows"
)

var authCancelSynchronousIO = windows.NewLazySystemDLL("kernel32.dll").NewProc("CancelSynchronousIo")

func readAuthFile(ctx context.Context, file *os.File, read func(io.Reader) (string, error)) (string, error) {
	var value string
	var err error
	ready := make(chan windows.Handle)
	done := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		// Keep the OS thread reserved until the caller stops issuing native
		// cancellation; otherwise it could cancel an unrelated read after reuse.
		defer func() { close(done); <-release }()
		var thread windows.Handle
		thread, err = windows.OpenThread(windows.THREAD_TERMINATE, false, windows.GetCurrentThreadId())
		ready <- thread
		if err != nil {
			return
		}
		if err = ctx.Err(); err == nil {
			value, err = read(file)
		}
	}()
	thread := <-ready
	if thread != 0 {
		defer windows.CloseHandle(thread)
	}
	select {
	case <-done:
	case <-ctx.Done():
		// Cancellation can race the syscall's entry. Retry until the reader
		// has exited, covering synchronous console and overlapped pipe reads.
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			_, _, _ = authCancelSynchronousIO.Call(uintptr(thread))
			_ = windows.CancelIoEx(windows.Handle(file.Fd()), nil)
			select {
			case <-done:
				return "", ctx.Err()
			case <-ticker.C:
			}
		}
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return value, err
}
