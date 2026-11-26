package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/gitcli"
)

func TestResumeLegacyCloneLayout(t *testing.T) {
	s, store, remote := fixture(t)
	ctx := t.Context()
	if _, err := s.Git.(*gitcli.Client).Run(ctx, remote, "branch", "topic"); err != nil {
		t.Fatal(err)
	}
	op := &app.Operation{Version: 1, ID: "old-clone", Root: store.Root, Kind: "repo.add", Remote: remote, State: "partial", Steps: []app.Step{{Repository: "example", State: "started", RequestedRefFormat: "files"}}}
	if err := s.Journal.Save(op); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resume(ctx, op.ID); err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(store.Root, "repos", "example.git")
	got, err := s.Git.Resolve(ctx, bare, "refs/heads/topic")
	if err != nil || got == "" {
		t.Fatalf("legacy local branch = %q, %v", got, err)
	}
}

func TestRetainedLegacyCloneKeepsMovedTag(t *testing.T) {
	s, store, source := fixture(t)
	ctx := t.Context()
	prGit(t, source, "tag", "release")
	if err := store.EnsureDir("repos"); err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(store.Root, "repos", "example.git")
	prGit(t, source, "clone", "--bare", "--no-local", source, bare)
	old, err := s.Git.Resolve(ctx, bare, "refs/tags/release")
	if err != nil {
		t.Fatal(err)
	}
	prGit(t, source, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "new")
	prGit(t, source, "tag", "-f", "release")
	op := &app.Operation{Version: 1, ID: "legacy-tag", Root: store.Root, Kind: "repo.add", Remote: source, State: "partial", Steps: []app.Step{{Repository: "example", State: "started", RefFormat: "files"}}}
	if err := s.Journal.Save(op); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resume(ctx, op.ID); err != nil {
		t.Fatal(err)
	}
	if tip, err := s.Git.Resolve(ctx, bare, "refs/tags/release"); err != nil || tip != old {
		t.Fatalf("local tag changed: %s, %v", tip, err)
	}
}

type interruptBootstrap struct {
	*gitcli.Client
	path string
}

func (g *interruptBootstrap) CloneWithFormat(_ context.Context, _, path, _ string) error {
	g.path = path
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(path, "partial"), []byte("preserved"), 0600); err != nil {
		return err
	}
	return errors.New("interrupted before origin")
}

func TestResumeInterruptedPreOriginBootstrap(t *testing.T) {
	s, store, remote := fixture(t)
	ctx := t.Context()
	client := s.Git.(*gitcli.Client)
	stopped := &interruptBootstrap{Client: client}
	s.Git = stopped
	result, err := s.AddRepository(ctx, "api", remote, false)
	if err == nil || result.OperationID == "" || !strings.Contains(err.Error(), "interrupted before origin") {
		t.Fatalf("unexpected setup: %+v %v", result, err)
	}
	s.Git = client
	if _, err := s.Resume(ctx, result.OperationID); err != nil {
		t.Fatal(err)
	}
	final := filepath.Join(store.Root, "repos", "api.git")
	if stopped.path == final {
		t.Fatal("unverified pre-origin bytes reached the published destination")
	}
	if content, err := os.ReadFile(filepath.Join(stopped.path, "partial")); err != nil || string(content) != "preserved" {
		t.Fatalf("partial data changed: %q %v", content, err)
	}
	if got, err := client.Config(ctx, final, "remote.origin.url"); err != nil || got != remote {
		t.Fatalf("wrong published origin: %s %v", got, err)
	}
}

func TestResumeBootstrapRefusesForeignFinalDestination(t *testing.T) {
	s, store, remote := fixture(t)
	ctx := t.Context()
	client := s.Git.(*gitcli.Client)
	s.Git = &interruptBootstrap{Client: client}
	result, err := s.AddRepository(ctx, "api", remote, false)
	if err == nil {
		t.Fatal("expected interrupted bootstrap")
	}
	final := filepath.Join(store.Root, "repos", "api.git")
	prGit(t, remote, "clone", "--bare", remote, final)
	s.Git = client
	if _, err = s.Resume(ctx, result.OperationID); err == nil || !strings.Contains(err.Error(), "ownership") {
		t.Fatalf("accepted replacement repository: %v", err)
	}
	catalog, _, err := store.LoadCatalog()
	if err != nil || len(catalog.Repositories) != 0 {
		t.Fatalf("unexpected registration: %+v %v", catalog, err)
	}
}

type interruptAfterOrigin struct {
	*gitcli.Client
	path string
}

func (g *interruptAfterOrigin) CloneWithFormat(ctx context.Context, remote, path, _ string) error {
	g.path = path
	if _, err := g.Run(ctx, "", "init", "--bare", "-b", "unresolved", path); err != nil {
		return err
	}
	if _, err := g.Run(ctx, path, "remote", "add", "origin", remote); err != nil {
		return err
	}
	return errors.New("interrupted after origin")
}
func TestResumeIncompleteDefaultBranchTransfer(t *testing.T) {
	s, store, remote := fixture(t)
	ctx := t.Context()
	prGit(t, remote, "branch", "-m", "develop")
	core := s.Git.(*gitcli.Client)
	interrupted := &interruptAfterOrigin{Client: core}
	s.Git = interrupted
	result, err := s.AddRepository(ctx, "api", remote, false)
	if err == nil || !strings.Contains(err.Error(), "interrupted after origin") {
		t.Fatal(err)
	}
	s.Git = core
	if _, err := s.Resume(ctx, result.OperationID); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.Root, "repos", "api.git")
	oid, err := core.Resolve(ctx, path, "refs/heads/develop")
	if err != nil {
		t.Fatalf("lost default branch during resume: %v", err)
	}
	upstream, err := core.Resolve(ctx, path, "develop@{upstream}")
	if err != nil || upstream != oid {
		t.Fatalf("upstream=%q want=%q err=%v", upstream, oid, err)
	}
}
