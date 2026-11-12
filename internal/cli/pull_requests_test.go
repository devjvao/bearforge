package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bearforge/internal/app"
	"bearforge/internal/config"
	"bearforge/internal/domain"
	"bearforge/internal/github"
)

func TestPRListHelp(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run(t.Context(), []string{"pr", "list", "--help"}, strings.NewReader(""), &out, &errOut)
	if code != 0 || !strings.Contains(out.String(), "list WORKSPACE") || !strings.Contains(out.String(), "--refresh") || !strings.Contains(out.String(), "--state") {
		t.Fatalf("missing PR command: code=%d out=%s err=%s", code, out.String(), errOut.String())
	}
}

type cliPRProvider struct {
	lookup func(context.Context, domain.PullRequestQuery, bool) (domain.PullRequestObservation, error)
}

func (p cliPRProvider) Resolve(r domain.Repository, remote, branch string) (domain.PullRequestQuery, error) {
	return github.NewClient().Resolve(r, remote, branch)
}
func (p cliPRProvider) Lookup(ctx context.Context, q domain.PullRequestQuery, refresh bool) (domain.PullRequestObservation, error) {
	return p.lookup(ctx, q, refresh)
}

func TestPRListOutputsAndSelection(t *testing.T) {
	root := cliExecRoot(t)
	store := config.New(root)
	c, rev, err := store.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for id, r := range c.Repositories {
		r.Provider = map[string]string{"kind": "github", "host": "github.com", "repository": "org/" + id, "source_repository": "fork/" + id}
		url := "https://github.com/fork/" + id + ".git"
		r.Remotes[c.Remote] = url
		command := exec.Command("git", "-C", filepath.Join(root, "repos", id+".git"), "config", "remote."+c.Remote+".url", url)
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("configure fixture: %v %s", err, out)
		}
		c.Repositories[id] = r
	}
	if err := store.SaveCatalog(c, rev); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"human", "json", "jsonl"} {
		t.Run(mode, func(t *testing.T) {
			var out, errOut bytes.Buffer
			o := &options{in: strings.NewReader(""), out: &out, errOut: &errOut, pullRequestProvider: cliPRProvider{lookup: func(_ context.Context, q domain.PullRequestQuery, refresh bool) (domain.PullRequestObservation, error) {
				if q.Repository != "org/web" || q.Base != "main" || q.State != "closed" || !refresh {
					t.Fatalf("wrong CLI query: %+v refresh=%v", q, refresh)
				}
				return domain.PullRequestObservation{Query: q, ObservedAt: time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC), Cached: true, PullRequests: []domain.PullRequest{{Number: 42, Title: "safe\x1b[2J title", URL: "https://github.com/org/web/pull/42", State: "merged", Draft: true, SourceRepository: "fork/web", TargetRepository: "org/web", SourceBranch: "review", TargetBranch: "main", UpdatedAt: time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)}}}, nil
			}}}
			cmd := newRoot(o)
			cmd.SetArgs([]string{"--root", root, "--output=" + mode, "pr", "list", "review", "--repo", "web", "--state", "closed", "--base", "main", "--refresh"})
			if err := cmd.ExecuteContext(t.Context()); err != nil {
				t.Fatalf("execute: %v out=%s err=%s", err, out.String(), errOut.String())
			}
			if errOut.Len() != 0 || strings.Contains(out.String(), "\x1b") {
				t.Fatalf("unsafe output: %q stderr=%q", out.String(), errOut.String())
			}
			if mode == "human" {
				for _, want := range []string{"web", "review", "main", "42", "safe title", "merged", "https://github.com/org/web/pull/42", "2026-11-12T12:00:00Z", "true", "fork/web", "org/web", "2026-11-11T12:00:00Z"} {
					if !strings.Contains(out.String(), want) {
						t.Fatalf("human output missing %q: %s", want, out.String())
					}
				}
				return
			}
			var result struct {
				app.Result
				Type     string `json:"type"`
				Sequence int    `json:"sequence"`
			}
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Command != "pr.list" || len(result.Items) != 1 || result.Items[0].Repository != "web" || result.Items[0].PullRequests == nil || !result.Items[0].PullRequests.Cached || result.Items[0].PullRequests.PullRequests[0].Number != 42 {
				t.Fatalf("lost PR details: %s", out.String())
			}
			if mode == "jsonl" && (result.Type != "result" || result.Sequence != 1) {
				t.Fatalf("wrong envelope: %s", out.String())
			}
		})
	}
}

func TestPRHumanOutputIncludesSkippedErrors(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var out, errOut bytes.Buffer
	o := &options{out: &out, errOut: &errOut, color: "never"}
	r := app.NewResult("pr.list")
	r.Items = []app.Item{{Repository: "api", Branch: "actual", ExpectedBranch: "intended", Outcome: "none", PullRequests: &domain.PullRequestObservation{ObservedAt: time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC), PullRequests: []domain.PullRequest{}}}, {Repository: "detached", Outcome: "detached"}, {Repository: "unborn", Outcome: "unborn"}, {Repository: "bad\x1b[2J", Outcome: "failed", Problem: &app.Problem{Code: "offline", Message: "retry\x1b[2J later"}}}
	if err := o.renderPullRequests(r, true); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"actual", "intended", "none", "detached", "unborn", "offline", "retry later", "2026-11-12T12:00:00Z", "false"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q: %q", want, out.String())
		}
	}
	if strings.Contains(out.String()+errOut.String(), "\x1b") {
		t.Fatalf("terminal injection: %q %q", out.String(), errOut.String())
	}
	reader, writer := io.Pipe()
	_ = reader.Close()
	defer writer.Close()
	o.out = writer
	if err := o.renderPullRequests(r, false); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("lost write failure: %v", err)
	}
}

func TestPRHumanOutputWrapsCompleteExternalValues(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var out, errOut bytes.Buffer
	o := &options{out: &out, errOut: &errOut, color: "never"}
	title := "Complete" + strings.Repeat("x", 160) + "Title"
	url := "https://github.com/org/repository/pull/123456789"
	r := app.NewResult("pr.list")
	r.Items = []app.Item{{Repository: "api", Outcome: "found", PullRequests: &domain.PullRequestObservation{PullRequests: []domain.PullRequest{{Number: 123456789, Title: title, URL: url}}}}}
	if err := o.renderPullRequests(r, true); err != nil {
		t.Fatal(err)
	}
	compact := strings.ReplaceAll(out.String(), "\n", "")
	if !strings.Contains(compact, title) || !strings.Contains(compact, url) {
		t.Fatalf("external values truncated: %q", out.String())
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if len(line) > 80 {
			t.Fatalf("overlong line: %q", line)
		}
	}
}

func TestPRListCancellationExitCode(t *testing.T) {
	root := cliExecRoot(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var out, errOut bytes.Buffer
	if code := Run(ctx, []string{"--root", root, "--json", "pr", "list", "review"}, strings.NewReader(""), &out, &errOut); code != 130 {
		t.Fatalf("code=%d out=%s err=%s", code, out.String(), errOut.String())
	}
	var result app.Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || len(result.Items) != 2 {
		t.Fatalf("lost cancelled members: %s %v", out.String(), err)
	}
}
