//go:build !windows

package cli

import "os/exec"

func configureAuthBrowser(*exec.Cmd) {}
