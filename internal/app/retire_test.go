package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"bearforge/internal/app"
	"bearforge/internal/config"
	"bearforge/internal/domain"
)

func retirementFixture(t *testing.T) (*app.Service, *config.Store, string) {
	t.Helper()
	s, store, remote := fixture(t)
	if _, err := s.AddRepository(t.Context(), "api", remote, false); err != nil {
		t.Fatal(err)
	}
	return s, store, remote
}

func TestRepositoryRetireRestorePreservesHistoryAndRegistration(t *testing.T) {
	s, store, remote := retirementFixture(t)
	bare := filepath.Join(store.Root, "repos", "api.git")
	head, err := s.Git.Resolve(t.Context(), bare, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	before := dashboardFiles(t, store.Root)
	req := app.RetireRequest{Repository: "api", DryRun: true}
	preview, err := s.RetireRepository(t.Context(), req)
	if err != nil || preview.PlanDigest == "" {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	plan := preview.Data.(app.RepositoryPlan)
	if plan.Source != bare || plan.Destination != filepath.Join(store.Root, ".bearforge", "trash", plan.Operation, "api.git") {
		t.Fatalf("nonconcrete plan: %+v", plan)
	}
	if !reflect.DeepEqual(before, dashboardFiles(t, store.Root)) {
		t.Fatal("retirement preview changed files")
	}
	req.DryRun, req.ExpectedPlan, req.PlanOperation = false, preview.PlanDigest, plan.Operation
	retired, err := s.RetireRepository(t.Context(), req)
	if err != nil || retired.OperationID != plan.Operation {
		t.Fatalf("retire: %+v %v", retired, err)
	}
	if oid, err := s.Git.Resolve(t.Context(), plan.Destination, "HEAD"); err != nil || oid != head {
		t.Fatal("retirement lost history")
	}
	c, _, err := store.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Repositories["api"]; ok || c.Retired["api"].Operation != retired.OperationID {
		t.Fatalf("retired catalog: %+v", c)
	}
	for _, id := range []string{"api", "duplicate"} {
		if _, err := s.AddRepository(t.Context(), id, remote, false); err == nil {
			t.Fatal("retired identity was silently cloned again")
		}
	}
	restore := app.RestoreRequest{Repository: "api", Operation: retired.OperationID, DryRun: true}
	preview, err = s.RestoreRepository(t.Context(), restore)
	if err != nil {
		t.Fatal(err)
	}
	restorePlan := preview.Data.(app.RepositoryPlan)
	restore.DryRun, restore.ExpectedPlan, restore.PlanOperation = false, preview.PlanDigest, restorePlan.Operation
	if _, err := s.RestoreRepository(t.Context(), restore); err != nil {
		t.Fatal(err)
	}
	if oid, err := s.Git.Resolve(t.Context(), bare, "HEAD"); err != nil || oid != head {
		t.Fatal("restore lost history")
	}
	c, _, err = store.LoadCatalog()
	if err != nil || len(c.Retired) != 0 || c.Repositories["api"].Remotes["origin"] != remote {
		t.Fatalf("restored registration: %+v %v", c, err)
	}
}

func TestRetirementRefusesWorkspaceReferencesAndLinkedWorktrees(t *testing.T) {
	s, store, _ := retirementFixture(t)
	if _, err := s.CreateWorkspace(t.Context(), "review", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRepositories(t.Context(), app.AddRequest{Workspace: "review", Repositories: []string{"api"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RetireRepository(t.Context(), app.RetireRequest{Repository: "api", DryRun: true}); err == nil {
		t.Fatal("retired a referenced repository")
	}
	w, rev, err := store.LoadWorkspace("review")
	if err != nil {
		t.Fatal(err)
	}
	w.Repositories = map[string]domain.Mapping{}
	if err := store.SaveWorkspace(w, rev); err != nil {
		t.Fatal(err)
	}
	before := dashboardFiles(t, store.Root)
	if _, err := s.RetireRepository(t.Context(), app.RetireRequest{Repository: "api"}); err == nil {
		t.Fatal("retired linked worktree after its mapping disappeared")
	}
	if !reflect.DeepEqual(before, dashboardFiles(t, store.Root)) {
		t.Fatal("refused retirement changed repository or journal")
	}
}

type retirementInterruptedStore struct {
	*config.Store
	failSave bool
}

func (s *retirementInterruptedStore) SaveCatalog(c domain.Catalog, revision string) error {
	if s.failSave {
		return errors.New("injected metadata replacement failure")
	}
	return s.Store.SaveCatalog(c, revision)
}

func TestRetireAndRestoreRecoverAfterRenameBeforeMetadata(t *testing.T) {
	s, store, _ := retirementFixture(t)
	failing := &retirementInterruptedStore{Store: store, failSave: true}
	s.Store = failing
	r, err := s.RetireRepository(t.Context(), app.RetireRequest{Repository: "api"})
	if err == nil || r.OperationID == "" || r.Outcome != "partial" {
		t.Fatalf("rename interruption: %+v %v", r, err)
	}
	trash := filepath.Join(store.Root, ".bearforge", "trash", r.OperationID, "api.git")
	if _, err := os.Stat(trash); err != nil {
		t.Fatal("opaque repository was lost")
	}
	c, _, err := store.LoadCatalog()
	if err != nil || len(c.Repositories) != 1 {
		t.Fatal("registration was lost before metadata succeeded")
	}
	failing.failSave = false
	retired, err := s.Resume(t.Context(), r.OperationID)
	if err != nil {
		t.Fatalf("retirement recovery: %v", err)
	}
	failing.failSave = true
	r, err = s.RestoreRepository(t.Context(), app.RestoreRequest{Repository: "api", Operation: retired.OperationID})
	if err == nil || r.OperationID == "" || r.Outcome != "partial" {
		t.Fatalf("restore interruption: %+v %v", r, err)
	}
	failing.failSave = false
	if _, err := s.Resume(t.Context(), r.OperationID); err != nil {
		t.Fatalf("restore recovery: %v", err)
	}
	if _, err := s.Git.Resolve(t.Context(), filepath.Join(store.Root, "repos", "api.git"), "HEAD"); err != nil {
		t.Fatal("history missing after both recovery drills")
	}
}

func TestRetirementRefusesStaleDefinitionAndOccupiedDestination(t *testing.T) {
	for _, drift := range []string{"catalog", "destination"} {
		t.Run(drift, func(t *testing.T) {
			s, store, _ := retirementFixture(t)
			preview, err := s.RetireRepository(t.Context(), app.RetireRequest{Repository: "api", DryRun: true})
			if err != nil {
				t.Fatal(err)
			}
			plan := preview.Data.(app.RepositoryPlan)
			if drift == "catalog" {
				c, revision, err := store.LoadCatalog()
				if err != nil {
					t.Fatal(err)
				}
				c.Jobs = 3
				if err := store.SaveCatalog(c, revision); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := store.CreateDir(".bearforge", "trash", plan.Operation, "api.git"); err != nil {
					t.Fatal(err)
				}
			}
			before := dashboardFiles(t, store.Root)
			if _, err := s.RetireRepository(t.Context(), app.RetireRequest{Repository: "api", ExpectedPlan: preview.PlanDigest, PlanOperation: plan.Operation}); err == nil {
				t.Fatal("stale or occupied plan accepted")
			}
			if !reflect.DeepEqual(before, dashboardFiles(t, store.Root)) {
				t.Fatal("refused confirmation changed history or journal")
			}
		})
	}
}

type retirementFinalizationJournal struct {
	app.Journal
	fail bool
}

func (j *retirementFinalizationJournal) Save(op *app.Operation) error {
	if j.fail && (op.Kind == "repo.retire" || op.Kind == "repo.restore") && op.State == "complete" {
		j.fail = false
		return errors.New("injected final journal failure")
	}
	return j.Journal.Save(op)
}

func TestRetirementRecoversAfterCommittedMetadataAndJournalFailure(t *testing.T) {
	s, store, _ := retirementFixture(t)
	journal := &retirementFinalizationJournal{Journal: s.Journal, fail: true}
	s.Journal = journal
	r, err := s.RetireRepository(t.Context(), app.RetireRequest{Repository: "api"})
	if err == nil || r.OperationID == "" {
		t.Fatalf("journal failure missing: %+v %v", r, err)
	}
	c, _, err := store.LoadCatalog()
	if err != nil || len(c.Retired) != 1 {
		t.Fatal("retirement metadata was not committed before injected failure")
	}
	if _, err := s.Resume(t.Context(), r.OperationID); err != nil {
		t.Fatalf("committed retirement recovery: %v", err)
	}
	journal.fail = true
	restored, err := s.RestoreRepository(t.Context(), app.RestoreRequest{Repository: "api", Operation: r.OperationID})
	if err == nil || restored.OperationID == "" {
		t.Fatalf("restore finalization failure missing: %+v %v", restored, err)
	}
	if _, err := s.Resume(t.Context(), restored.OperationID); err != nil {
		t.Fatalf("committed restoration recovery: %v", err)
	}
}

type retirementMoveStore struct {
	*config.Store
	cancel   context.CancelFunc
	failMove bool
}

func (s *retirementMoveStore) MoveDirectory(from, to []string) error {
	if s.failMove {
		return errors.New("injected sharing violation")
	}
	if err := s.Store.MoveDirectory(from, to); err != nil {
		return err
	}
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}

func TestRetirementMoveFailureAndCancellationPreserveRecovery(t *testing.T) {
	for _, mode := range []string{"move failed", "cancel after move"} {
		t.Run(mode, func(t *testing.T) {
			s, store, _ := retirementFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			moving := &retirementMoveStore{Store: store, failMove: mode == "move failed"}
			if mode == "cancel after move" {
				moving.cancel = cancel
			}
			s.Store = moving
			r, err := s.RetireRepository(ctx, app.RetireRequest{Repository: "api"})
			if err == nil || r.OperationID == "" || r.Outcome != "partial" {
				t.Fatalf("interruption: %+v %v", r, err)
			}
			if mode == "cancel after move" && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
			c, _, err := store.LoadCatalog()
			if err != nil || len(c.Repositories) != 1 {
				t.Fatal("registration lost before successful metadata replacement")
			}
			moving.cancel, moving.failMove = nil, false
			if _, err := s.Resume(t.Context(), r.OperationID); err != nil {
				t.Fatalf("move recovery: %v", err)
			}
		})
	}
}

func TestRetirementRefusesChangedRefsAfterPreview(t *testing.T) {
	s, store, _ := retirementFixture(t)
	preview, err := s.RetireRepository(t.Context(), app.RetireRequest{Repository: "api", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	plan := preview.Data.(app.RepositoryPlan)
	runner := s.Git.(interface {
		Run(context.Context, string, ...string) ([]byte, error)
	})
	if _, err := runner.Run(t.Context(), filepath.Join(store.Root, "repos", "api.git"), "branch", "new-local-branch", "HEAD"); err != nil {
		t.Fatal(err)
	}
	before := dashboardFiles(t, store.Root)
	if _, err := s.RetireRepository(t.Context(), app.RetireRequest{Repository: "api", ExpectedPlan: preview.PlanDigest, PlanOperation: plan.Operation}); err == nil {
		t.Fatal("changed local refs accepted without a new preview")
	}
	if !reflect.DeepEqual(before, dashboardFiles(t, store.Root)) {
		t.Fatal("stale ref approval changed repository or journal")
	}
}

func TestDoctorInspectsRetiredStorage(t *testing.T) {
	s, store, _ := retirementFixture(t)
	r, err := s.RetireRepository(t.Context(), app.RetireRequest{Repository: "api"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Doctor(t.Context(), ""); err != nil {
		t.Fatalf("intact retired storage is unhealthy: %v", err)
	}
	trash := filepath.Join(store.Root, ".bearforge", "trash", r.OperationID, "api.git")
	if err := os.Rename(trash, trash+"-moved-aside"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Doctor(t.Context(), ""); err == nil {
		t.Fatal("missing retired history was reported healthy")
	}
}
