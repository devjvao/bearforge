package app_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"bearforge/internal/app"
	"bearforge/internal/config"
	"bearforge/internal/domain"
	"bearforge/internal/operation"
)

func TestDashboardEmptyRootDoesNotCreateFiles(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "missing")
	store := config.New(root)
	s := app.New(store, nil, operation.New(store))
	snapshot, err := s.Dashboard(t.Context(), "")
	if err != nil || snapshot.Workspace != "" || len(snapshot.Workspaces) != 0 {
		t.Fatalf("unexpected empty dashboard: %+v, %v", snapshot, err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("dashboard created root: %v", err)
	}
	_, err = s.Dashboard(t.Context(), "../outside")
	var invalid *domain.Error
	if !errors.As(err, &invalid) || invalid.Code != "invalid" {
		t.Fatalf("invalid workspace accepted: %v", err)
	}
	_, err = s.Dashboard(t.Context(), "missing")
	if !errors.As(err, &invalid) || invalid.Code != "not_found" {
		t.Fatalf("missing workspace accepted: %v", err)
	}
}

func TestDashboardObservesOnlySelectedWorkspaceWithoutWrites(t *testing.T) {
	s, store, remote := fixture(t)
	ctx := t.Context()
	if _, err := s.AddRepository(ctx, "api", remote, false); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"zulu", "alpha"} {
		if _, err := s.CreateWorkspace(ctx, name, "", false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AddRepositories(ctx, app.AddRequest{Workspace: "zulu", Repositories: []string{"api"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.Root, "zulu", "api", "notes.txt"), []byte("keep me"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"complete", "abandoned", "partial", "running"} {
		if err := s.Journal.Save(&app.Operation{Version: 1, ID: state, Root: store.Root, Kind: "workspace.add", Workspace: "zulu", State: state}); err != nil {
			t.Fatal(err)
		}
	}
	before := dashboardFiles(t, store.Root)
	snapshot, err := s.Dashboard(ctx, "")
	if err != nil || snapshot.Workspace != "alpha" || !reflect.DeepEqual(snapshot.Workspaces, []string{"alpha", "zulu"}) || len(snapshot.Items) != 0 {
		t.Fatalf("default selection: %+v, %v", snapshot, err)
	}
	snapshot, err = s.Dashboard(ctx, "zulu")
	if err != nil || len(snapshot.Items) != 1 || snapshot.Items[0].Outcome != "changes" || snapshot.Items[0].Status.Untracked != 1 {
		t.Fatalf("live status: %+v, %v", snapshot, err)
	}
	if len(snapshot.PendingOperations) != 2 || snapshot.PendingOperations[0].ID != "partial" || snapshot.PendingOperations[1].ID != "running" {
		t.Fatalf("unfinished operations not preserved: %+v", snapshot.PendingOperations)
	}
	if snapshot.ObservedAt.IsZero() || snapshot.Root != store.Root {
		t.Fatalf("missing observation context: %+v", snapshot)
	}
	if after := dashboardFiles(t, store.Root); !reflect.DeepEqual(before, after) {
		t.Fatal("dashboard changed workspace files, refs, index or journals")
	}
}

func TestDashboardKeepsPartialStatusAndReportsJournalFailure(t *testing.T) {
	s, store, remote := fixture(t)
	ctx := t.Context()
	if _, err := s.AddRepository(ctx, "api", remote, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateWorkspace(ctx, "feature", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRepositories(ctx, app.AddRequest{Workspace: "feature", Repositories: []string{"api"}}); err != nil {
		t.Fatal(err)
	}
	w, rev, err := store.LoadWorkspace("feature")
	if err != nil {
		t.Fatal(err)
	}
	w.Repositories["missing"] = domain.Mapping{Branch: "feature"}
	if err := store.SaveWorkspace(w, rev); err != nil {
		t.Fatal(err)
	}
	if err := store.Write([]string{".bearforge", "state", "operations", "broken.json"}, []byte("{")); err != nil {
		t.Fatal(err)
	}
	if err := s.Journal.Save(&app.Operation{Version: 1, ID: "pending", Root: store.Root, Kind: "workspace.add", Workspace: "feature", State: "partial"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.Dashboard(ctx, "feature")
	if err != nil || len(snapshot.Items) != 2 || snapshot.Items[0].Outcome != "clean" || snapshot.Items[1].Outcome != "unreadable" {
		t.Fatalf("partial status hidden: %+v, %v", snapshot, err)
	}
	if len(snapshot.Problems) != 2 || !strings.Contains(snapshot.Problems[0].Message, "operations") {
		t.Fatalf("observation failures hidden: %+v", snapshot.Problems)
	}
	if len(snapshot.PendingOperations) != 1 || snapshot.PendingOperations[0].ID != "pending" {
		t.Fatalf("readable pending operation hidden: %+v", snapshot.PendingOperations)
	}
}

func TestDashboardHonorsCancellation(t *testing.T) {
	store := config.New(filepath.Join(t.TempDir(), "missing"))
	s := app.New(store, nil, operation.New(store))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Dashboard(ctx, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read returned %v", err)
	}
}

func dashboardFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files[path] = config.Revision(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}
