package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/domain"
)

type failingIntent struct {
	app.IntentStore
	fail bool
}

type cancelAbandonJournal struct {
	app.Journal
	cancel context.CancelFunc
	phase  string
}

func (j cancelAbandonJournal) List() ([]app.Operation, error) {
	ops, err := j.Journal.List()
	if j.phase == "scan" {
		j.cancel()
	}
	return ops, err
}

func (j cancelAbandonJournal) Load(id string) (*app.Operation, error) {
	op, err := j.Journal.Load(id)
	if j.phase == "load" {
		j.cancel()
	}
	return op, err
}

type cancelPendingScan struct{ cancelAbandonJournal }

func (j cancelPendingScan) PendingSummaries(ctx context.Context) ([]app.OperationSummary, error) {
	j.cancel()
	return nil, ctx.Err()
}

func TestAbandonHonorsCancellationBeforePublication(t *testing.T) {
	for _, phase := range []string{"scan", "pending summaries", "load"} {
		t.Run(phase, func(t *testing.T) {
			s, _, _ := fixture(t)
			result, err := s.CreateWorkspace(t.Context(), "review", "", false)
			if err != nil {
				t.Fatal(err)
			}
			journal := s.Journal
			op, err := journal.Load(result.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			op.State = "partial"
			if err := journal.Save(op); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			wrapped := cancelAbandonJournal{journal, cancel, phase}
			s.Journal = wrapped
			if phase == "pending summaries" {
				s.Journal = cancelPendingScan{wrapped}
			}
			if _, err := s.Abandon(ctx, op.ID); !errors.Is(err, context.Canceled) {
				t.Errorf("abandon after cancellation: %v", err)
			}
			got, err := journal.Load(op.ID)
			if err != nil || got.State != "partial" {
				t.Fatalf("lost resumable journal: %+v, %v", got, err)
			}
			unlock, err := journal.Lock(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if err := unlock(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestResumeRemovedWorkspaceAfterManifestDeletion(t *testing.T) {
	s, store, remote := fixture(t)
	ctx := context.Background()
	s.AddRepository(ctx, "api", remote, false)
	s.CreateWorkspace(ctx, "feature", "", false)
	s.AddRepositories(ctx, app.AddRequest{Workspace: "feature", Repositories: []string{"api"}})
	r, err := s.RemoveRepositories(ctx, app.RemoveRequest{Workspace: "feature", Whole: true})
	if err != nil {
		t.Fatal(err)
	}
	op, err := s.Journal.Load(r.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	op.State = "partial"
	if err := s.Journal.Save(op); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resume(ctx, op.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.LoadWorkspace("feature"); err == nil {
		t.Fatal("recreated removed manifest")
	}
}
func TestCreateRecoveryRefusesUnrelatedDirectory(t *testing.T) {
	s, store, _ := fixture(t)
	ctx := context.Background()
	r, err := s.CreateWorkspace(ctx, "feature", "", false)
	if err != nil {
		t.Fatal(err)
	}
	op, err := s.Journal.Load(r.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	_, rev, err := store.LoadWorkspace("feature")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteWorkspace("feature", rev); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.Root, "feature", "unrelated.txt"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	op.State = "partial"
	s.Journal.Save(op)
	if _, err := s.Resume(ctx, op.ID); err == nil {
		t.Fatal("adopted unrelated nonempty directory")
	}
}

func (s *failingIntent) SaveWorkspace(w domain.Workspace, revision string) error {
	if s.fail {
		return errors.New("injected metadata failure")
	}
	return s.IntentStore.SaveWorkspace(w, revision)
}
func TestResumeAfterGitSuccessBeforeMetadata(t *testing.T) {
	s, store, remote := fixture(t)
	ctx := context.Background()
	if _, err := s.AddRepository(ctx, "api", remote, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateWorkspace(ctx, "feature", "", false); err != nil {
		t.Fatal(err)
	}
	failing := &failingIntent{IntentStore: store, fail: true}
	s.Store = failing
	result, err := s.AddRepositories(ctx, app.AddRequest{Workspace: "feature", Repositories: []string{"api"}})
	if err == nil || result.OperationID == "" {
		t.Fatal("missing recoverable failure")
	}
	failing.fail = false
	if _, err := s.Resume(ctx, result.OperationID); err != nil {
		t.Fatal(err)
	}
	w, _, err := store.LoadWorkspace("feature")
	if err != nil || w.Repositories["api"].Branch != "feature" {
		t.Fatalf("recovery failed: %+v %v", w, err)
	}
}

func TestResumeAfterWorktreeRemovalBeforeMetadata(t *testing.T) {
	s, store, remote := fixture(t)
	ctx := context.Background()
	if _, err := s.AddRepository(ctx, "api", remote, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateWorkspace(ctx, "feature", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRepositories(ctx, app.AddRequest{Workspace: "feature", Repositories: []string{"api"}}); err != nil {
		t.Fatal(err)
	}
	failing := &failingIntent{IntentStore: store, fail: true}
	s.Store = failing
	r, err := s.RemoveRepositories(ctx, app.RemoveRequest{Workspace: "feature", Repositories: []string{"api"}})
	if err == nil || r.OperationID == "" {
		t.Fatal("expected resumable metadata failure")
	}
	failing.fail = false
	if _, err := s.Resume(ctx, r.OperationID); err != nil {
		t.Fatal(err)
	}
	w, _, err := store.LoadWorkspace("feature")
	if err != nil || len(w.Repositories) != 0 {
		t.Fatalf("membership not repaired: %v", err)
	}
}
