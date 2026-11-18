// Package gitcli implements Git operations exclusively through the Git executable.
package gitcli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/devjvao/bearforge/internal/domain"
)

type Client struct {
	executable    string
	Timeout       time.Duration
	globalConfig  string
	noSystem      bool
	commandConfig []string
}
type CommandError struct {
	ExitCode  int
	Operation string
	Detail    string
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("git %s failed (exit %d): %s", e.Operation, e.ExitCode, e.Detail)
}

func New() (*Client, error) {
	p, err := exec.LookPath("git")
	if err != nil {
		return nil, domain.E("unavailable", "Git is not available on PATH")
	}
	c := &Client{executable: p, Timeout: 2 * time.Minute}
	b, err := c.Run(context.Background(), "", "--version")
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(string(b))
	if len(fields) < 3 {
		return nil, domain.E("unavailable", "unable to identify Git version")
	}
	v := strings.Split(fields[2], ".")
	if len(v) < 2 {
		return nil, domain.E("unavailable", "unrecognized Git version")
	}
	major, _ := strconv.Atoi(v[0])
	minor, _ := strconv.Atoi(v[1])
	if major < 2 || (major == 2 && minor < 40) {
		return nil, domain.E("unavailable", "Git 2.40 or newer is required")
	}
	return c, nil
}

type limitedBuffer struct {
	data     []byte
	limit    int
	exceeded bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - len(b.data)
	if len(p) > remaining {
		p = p[:remaining]
		b.exceeded = true
	}
	b.data = append(b.data, p...)
	return n, nil
}

func (c *Client) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	prefix := []string{"--no-pager", "--no-optional-locks"}
	prefix = append(prefix, c.commandConfig...)
	if dir != "" {
		prefix = append(prefix, "-C", dir)
	}
	cmd := exec.CommandContext(ctx, c.executable, append(prefix, args...)...)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(key)
		if upper == "GIT_DIR" || upper == "GIT_COMMON_DIR" || upper == "GIT_WORK_TREE" || upper == "GIT_INDEX_FILE" || upper == "GIT_OBJECT_DIRECTORY" || upper == "GIT_ALTERNATE_OBJECT_DIRECTORIES" || upper == "GIT_NAMESPACE" || strings.HasPrefix(upper, "GIT_CONFIG_") {
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "LC_ALL=C")
	if c.globalConfig != "" {
		cmd.Env = append(cmd.Env, "GIT_CONFIG_GLOBAL="+c.globalConfig)
	}
	if c.noSystem {
		cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1")
	}
	out := &limitedBuffer{limit: 16 << 20}
	stderr := &limitedBuffer{limit: 16 << 10}
	cmd.Stdout = out
	cmd.Stderr = stderr
	configureProcess(cmd)
	cmd.WaitDelay = 3 * time.Second
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if out.exceeded {
		return nil, domain.E("git_failed", "Git output exceeds safe parsing limit")
	}
	if err != nil {
		var exit *exec.ExitError
		code := -1
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		}
		operation := "command"
		if len(args) > 0 {
			operation = args[0]
		}
		return nil, &CommandError{ExitCode: code, Operation: operation, Detail: cleanError(string(stderr.data))}
	}
	return out.data, nil
}

func cleanError(s string) string {
	// Never return terminal control characters in diagnostics.
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < ' ' && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, s))
}

func isExit(err error, code int) bool {
	var e *CommandError
	return errors.As(err, &e) && e.ExitCode == code
}

func (c *Client) Clone(ctx context.Context, remote, path string) error {
	format, err := c.PlanClone(ctx, remote, path, "auto")
	if err != nil {
		return err
	}
	return c.CloneWithFormat(ctx, remote, path, format)
}

// Initialize is resumable only for a freshly cloned directory owned by a journal.
func (c *Client) Initialize(ctx context.Context, path string) error {
	if err := c.VerifyBare(ctx, path); err != nil {
		return err
	}
	for _, pair := range [][2]string{{"remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*"}, {"core.logAllRefUpdates", "true"}, {"push.default", "simple"}} {
		if _, err := c.Run(ctx, path, "config", "--local", "--replace-all", pair[0], pair[1]); err != nil {
			return err
		}
	}
	if err := c.Fetch(ctx, path); err != nil {
		return err
	}
	// The optional default alias must never replace a real tracking branch.
	// Remote branch names such as head, HEAD, and HEAD/child are valid refs.
	occupied, err := c.defaultAliasOccupied(ctx, path, "origin")
	if err != nil {
		return err
	}
	if !occupied {
		// Empty remotes can be registered without a default branch.
		if _, err := c.Run(ctx, path, "remote", "set-head", "origin", "-a"); err != nil && !isExit(err, 1) {
			return err
		}
	}
	b, err := c.Run(ctx, path, "for-each-ref", "--format=%(refname:strip=2)", "refs/heads/")
	if err != nil {
		return err
	}
	for _, branch := range strings.Fields(string(b)) {
		if _, err := c.Resolve(ctx, path, "refs/remotes/origin/"+branch); err != nil {
			continue
		}
		// Configure the literal ref name. The branch porcelain interprets HEAD
		// as the current branch even when refs/heads/HEAD exists independently.
		for _, pair := range [][2]string{{"branch." + branch + ".remote", "origin"}, {"branch." + branch + ".merge", "refs/heads/" + branch}} {
			if _, err := c.Run(ctx, path, "config", "--local", "--replace-all", pair[0], pair[1]); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Client) VerifyBare(ctx context.Context, path string) error {
	b, err := c.Run(ctx, path, "rev-parse", "--is-bare-repository")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(b)) != "true" {
		return domain.E("conflict", "expected bare repository at %s", path)
	}
	return nil
}

func (c *Client) Config(ctx context.Context, path, key string) (string, error) {
	b, err := c.Run(ctx, path, "config", "--get", key)
	if isExit(err, 1) {
		return "", nil
	}
	return strings.TrimSpace(string(b)), err
}

// RemoteURLs reads effective URLs with the same last-value semantics as Config.
func (c *Client) RemoteURLs(ctx context.Context, path string, names []string) (map[string]string, error) {
	urls := make(map[string]string, len(names))
	if len(names) > 1 {
		for _, name := range names {
			urls[name] = ""
		}
		// Catalog remote names contain only these characters. Restricting the
		// subsection also makes the key/value newline delimiter unambiguous.
		out, err := c.Run(ctx, path, "config", "--null", "--get-regexp", `^remote\.[a-zA-Z0-9_-]+\.url$`)
		if isExit(err, 1) {
			return urls, nil // No matching keys.
		}
		if err == nil {
			for record := range strings.SplitSeq(string(out), "\x00") {
				key, value, _ := strings.Cut(record, "\n")
				name := strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".url")
				if _, wanted := urls[name]; wanted {
					urls[name] = strings.TrimSpace(value)
				}
			}
			return urls, nil
		}
		// Run reports a parsing limit as git_failed and execution errors as
		// CommandError or a context error. The individual keys may still fit.
		var limit *domain.Error
		if !errors.As(err, &limit) || limit.Code != "git_failed" {
			return nil, err
		}
	}
	for _, name := range names {
		v, err := c.Config(ctx, path, "remote."+name+".url")
		if err != nil {
			return nil, err
		}
		urls[name] = v
	}
	return urls, nil
}

func (c *Client) ValidateFetch(ctx context.Context, path string) error {
	remotes, err := c.fetchRemotes(ctx, path)
	if err != nil {
		return err
	}
	keys := []string{"fetch.pruneTags"}
	for _, remote := range remotes {
		if err := domain.ValidateRemoteName(remote); err != nil {
			return err
		}
		keys = append(keys, "remote."+remote+".pruneTags", "remote."+remote+".mirror")
		refs, err := c.Run(ctx, path, "config", "--get-all", "remote."+remote+".fetch")
		if err != nil {
			return domain.E("conflict", "remote %s has no safe fetch mapping", remote)
		}
		for _, ref := range strings.Split(strings.TrimSpace(string(refs)), "\n") {
			if ref != "+refs/heads/*:refs/remotes/"+remote+"/*" && ref != "refs/heads/*:refs/remotes/"+remote+"/*" {
				return domain.E("conflict", "unsafe or unsupported fetch mapping for remote %s", remote)
			}
		}
	}
	for _, key := range keys {
		v, err := c.Run(ctx, path, "config", "--bool", "--get", key)
		if isExit(err, 1) {
			continue
		}
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(v)) == "true" {
			return domain.E("conflict", "unsafe Git setting %s=true; correct it before syncing", key)
		}
	}
	return nil
}

func (c *Client) Fetch(ctx context.Context, path string) error {
	if err := c.ValidateFetch(ctx, path); err != nil {
		return err
	}
	expected, err := c.fetchRefPlan(ctx, path)
	if err != nil {
		return err
	}
	var verificationErr error
	for attempt := 0; attempt < 3; attempt++ {
		if _, err := c.Run(ctx, path, "-c", "fetch.parallel=1", "fetch", "--all", "--prune"); err != nil {
			return err
		}
		// Separate advertisements are not the transfer's snapshot. Reconcile
		// changed names with another transfer rather than requiring their union.
		after, err := c.fetchRefPlan(ctx, path)
		if err != nil {
			return err
		}
		verificationErr = nil
		if sameRefNames(expected, after) {
			verificationErr = c.verifyFetchedRefs(ctx, path, after)
			if verificationErr == nil {
				return nil
			}
			// A branch can disappear for the transfer and be recreated before
			// the second advertisement, leaving equal names but a missing ref.
		}
		expected = after
	}
	if verificationErr != nil {
		return verificationErr
	}
	return domain.E("conflict", "remote ref names kept changing during fetch; preserve this directory and retry syncing after remote changes settle")
}

func (c *Client) Resolve(ctx context.Context, path, ref string) (string, error) {
	if ref == "" || strings.ContainsAny(ref, "\x00\r\n") {
		return "", domain.E("invalid", "empty or invalid ref")
	}
	b, err := c.Run(ctx, path, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	return strings.TrimSpace(string(b)), err
}

func (c *Client) BranchExists(ctx context.Context, path, branch string) (bool, error) {
	if err := c.ValidateBranch(ctx, branch); err != nil {
		return false, err
	}
	_, err := c.Run(ctx, path, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if isExit(err, 1) {
		return false, nil
	}
	return err == nil, err
}

func (c *Client) ValidateBranch(ctx context.Context, branch string) error {
	if err := domain.ValidateBranch(branch); err != nil {
		return err
	}
	if _, err := c.Run(ctx, "", "check-ref-format", "--branch", branch); err != nil {
		return domain.E("invalid", "invalid Git branch name %q", branch)
	}
	return nil
}

func (c *Client) RefExists(ctx context.Context, path, ref string) (bool, error) {
	if !strings.HasPrefix(ref, "refs/") || strings.ContainsAny(ref, "\x00\r\n") {
		return false, domain.E("invalid", "expected full Git ref")
	}
	_, err := c.Run(ctx, path, "show-ref", "--verify", "--quiet", ref)
	if isExit(err, 1) {
		return false, nil
	}
	return err == nil, err
}

func (c *Client) DefaultBase(ctx context.Context, path, remote string) (string, error) {
	if err := domain.ValidateRemoteName(remote); err != nil {
		return "", err
	}
	alias := "refs/remotes/" + remote + "/HEAD"
	b, err := c.Run(ctx, path, "symbolic-ref", "--quiet", alias)
	if err == nil {
		return strings.TrimSpace(string(b)), nil
	}
	names, symbolic, inventoryErr := c.refInventory(ctx, path)
	if inventoryErr != nil {
		return "", inventoryErr
	}
	// Lookup stays read-only: conservatively recognize a suppressed alias from
	// exact stored names without probing the filesystem during plan/inspection.
	if remote == "origin" && defaultAliasConflicts(names, symbolic, alias, false) {
		// A bare clone retains the remote default in its own HEAD. Use that
		// exact tracking ref when its name prevents creating origin/HEAD.
		b, err := c.Run(ctx, path, "symbolic-ref", "--quiet", "HEAD")
		if err == nil && strings.HasPrefix(strings.TrimSpace(string(b)), "refs/heads/") {
			ref := "refs/remotes/origin/" + strings.TrimPrefix(strings.TrimSpace(string(b)), "refs/heads/")
			if err := c.verifyFetchedRefs(ctx, path, []string{ref}); err == nil {
				return ref, nil
			}
		}
	}
	return "", domain.E("conflict", "remote default branch is unresolved; provide --base")
}
