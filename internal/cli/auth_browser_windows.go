package cli

import (
	"os/exec"
	"syscall"
)

func configureAuthBrowser(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }
