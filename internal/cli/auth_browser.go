package cli

import (
	"context"
	"errors"
	"io"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type authBrowser struct{}

func (authBrowser) Open(ctx context.Context, address string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || strings.ContainsAny(address, "\x00\r\n") {
		return errors.New("invalid browser authorization URL")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.CommandContext(ctx, "rundll32.exe", "url.dll,FileProtocolHandler", address)
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", address)
	default:
		cmd = exec.CommandContext(ctx, "xdg-open", address)
	}
	configureAuthBrowser(cmd)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Run(); err != nil {
		return errors.New("could not open browser; open the displayed URL manually")
	}
	return nil
}
