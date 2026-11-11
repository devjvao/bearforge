// Package process executes user-authorized commands with bounded capture and
// process-tree supervision. It preserves the user's environment.
package process

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"bearforge/internal/domain"
)

const captureLimit = 64 << 10

type Runner struct {
	Input         io.Reader
	Output, Error io.Writer
	Environment   []string
}

type capture struct {
	data      []byte
	truncated bool
	stream    string
	emit      func(domain.OutputChunk)
}

func (c *capture) Write(p []byte) (int, error) {
	n := len(p)
	remaining := captureLimit - len(c.data)
	c.data = append(c.data, p[:min(remaining, len(p))]...)
	if n > remaining {
		c.truncated = true
	}
	if c.emit != nil {
		for len(p) > 0 {
			size := min(4096, len(p))
			c.emit(domain.OutputChunk{Stream: c.stream, Encoding: "base64", Data: slices.Clone(p[:size])})
			p = p[size:]
		}
	}
	return n, nil
}

// Run drains stdout and stderr concurrently; emit may be called concurrently
// and must return promptly. All callbacks finish before Run returns.
func (r Runner) Run(ctx context.Context, spec domain.ProcessSpec, emit func(domain.OutputChunk)) (result domain.ProcessResult, err error) {
	result.Argv = slices.Clone(spec.Argv)
	result.ExitCode = -1
	result.Encoding = "base64"
	result.OutputCaptured = !spec.Interactive
	started := time.Now()
	defer func() { result.Duration = time.Since(started) }()
	if err := ctx.Err(); err != nil {
		result.Cancelled = true
		return result, err
	}
	if len(spec.Argv) == 0 || spec.Argv[0] == "" {
		return result, domain.E("invalid", "command is required")
	}
	if !filepath.IsAbs(spec.Directory) {
		return result, domain.E("invalid", "command directory must be absolute")
	}
	name := spec.Argv[0]
	if strings.ContainsAny(name, "/\\") && !filepath.IsAbs(name) {
		name = filepath.Join(spec.Directory, name)
	}
	if strings.EqualFold(filepath.Ext(name), ".cmd") || strings.EqualFold(filepath.Ext(name), ".bat") {
		return result, domain.E("invalid", "Windows command wrappers require --shell cmd")
	}
	cmd := exec.CommandContext(ctx, name, spec.Argv[1:]...)
	cmd.Dir = spec.Directory
	cmd.Env = cmd.Environ()
	if r.Environment != nil {
		cmd.Env = slices.Clone(r.Environment)
	}
	if spec.Interactive {
		if r.Input == nil || r.Output == nil || r.Error == nil {
			return result, domain.E("invalid", "interactive execution requires terminal streams")
		}
		cmd.Stdin = r.Input
	}
	out, stderr := &capture{stream: "stdout", emit: emit}, &capture{stream: "stderr", emit: emit}
	cmd.Stdout, cmd.Stderr = out, stderr
	if spec.Interactive {
		cmd.Stdout, cmd.Stderr = r.Output, r.Error
	}
	attach, cleanup, err := supervise(ctx, cmd)
	if err != nil {
		return result, err
	}
	defer func() {
		if cleanupErr := cleanup(); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("finish process supervision: %w", cleanupErr))
		}
	}()
	cmd.WaitDelay = 3 * time.Second
	if err := cmd.Start(); err != nil {
		if ctx.Err() != nil {
			result.Cancelled = true
			return result, ctx.Err()
		}
		return result, &domain.Error{Code: "unavailable", Message: fmt.Sprintf("start %s: %v", spec.Argv[0], err), Cause: err}
	}
	if err := attach(); err != nil {
		_ = cmd.Cancel()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if ctx.Err() != nil {
			result.Cancelled = true
			return result, ctx.Err()
		}
		return result, fmt.Errorf("supervise command process: %w", err)
	}
	if ctx.Err() != nil {
		_ = cmd.Cancel()
	}
	waitErr := cmd.Wait()
	result.Stdout, result.Stderr = out.data, stderr.data
	result.StdoutTruncated, result.StderrTruncated = out.truncated, stderr.truncated
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
		result.Signal = processSignal(cmd.ProcessState)
	}
	if err := ctx.Err(); err != nil {
		result.Cancelled = true
		return result, err
	}
	if spec.Interactive && processInterrupted(cmd.ProcessState) {
		result.Cancelled = true
		return result, context.Canceled
	}
	var exit *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exit) {
		return result, fmt.Errorf("wait for command: %w", waitErr)
	}
	return result, nil
}
