package operation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"bearforge/internal/app"
	"bearforge/internal/config"
)

func operationFixture(t *testing.T) (*config.Store, *Store) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	files := config.New(root)
	return files, New(files)
}

func TestLargeDiagnosticsRemainReadableWithoutChangingCaller(t *testing.T) {
	files, store := operationFixture(t)
	diagnostic := strings.Repeat("failed \"command\" λ\n", 2048)
	op := &app.Operation{Version: 1, ID: "large-errors", Root: files.Root, Kind: "exec", State: "complete"}
	for i := range 60 {
		op.Steps = append(op.Steps, app.Step{Repository: fmt.Sprintf("repo-%d", i), State: "failed", Error: diagnostic})
	}
	op.Error = strings.Repeat(diagnostic, len(op.Steps))
	if err := store.Save(op); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(op.ID)
	if err != nil {
		t.Fatalf("saved completed operation cannot be read: %v", err)
	}
	if loaded.State != "complete" || len(loaded.Steps) != 60 {
		t.Fatalf("operation identity/state lost: state=%s steps=%d", loaded.State, len(loaded.Steps))
	}
	for _, message := range []string{loaded.Error, loaded.Steps[0].Error} {
		if !strings.Contains(message, "[diagnostic truncated]") || !utf8.ValidString(message) {
			t.Fatal("bounded diagnostics must be valid UTF-8 and explicitly marked")
		}
	}
	if op.Error != strings.Repeat(diagnostic, 60) || op.Steps[0].Error != diagnostic {
		t.Fatal("saving a bounded snapshot changed the caller's diagnostics")
	}
	if _, err := store.List(); err != nil {
		t.Fatalf("completed operation blocks journal listing: %v", err)
	}
}

func TestSaveRejectsOversizedMetadataWithoutReplacingReadableSnapshot(t *testing.T) {
	files, store := operationFixture(t)
	op := &app.Operation{Version: 1, ID: "metadata", Root: files.Root, Kind: "exec", State: "running"}
	if err := store.Save(op); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(files.Root, ".bearforge", "state", "operations", op.ID+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	op.Exec = &app.ExecRequest{Argv: []string{strings.Repeat("x", 2<<20)}}
	if err := store.Save(op); err == nil {
		t.Fatal("saved metadata that the journal reader cannot load")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("rejected save replaced the readable snapshot: %v", err)
	}
	if _, err := store.Load(op.ID); err != nil {
		t.Fatalf("previous snapshot is unreadable: %v", err)
	}
	op.ID = "new-metadata"
	if err := store.Save(op); err == nil {
		t.Fatal("accepted an oversized initial journal")
	}
	if _, err := os.Stat(filepath.Join(files.Root, ".bearforge", "state", "operations", op.ID+".json")); !os.IsNotExist(err) {
		t.Fatalf("rejected initial journal created a file: %v", err)
	}
}

func TestSaveRoundTripsAtDocumentSizeBoundary(t *testing.T) {
	files, store := operationFixture(t)
	op := &app.Operation{Version: 1, ID: "boundary", Root: files.Root, Kind: "exec", State: "complete", Error: "x"}
	encoded, err := json.MarshalIndent(op, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	// Include the newline written by Save in the reader's 2 MiB budget.
	op.Error = strings.Repeat("x", (2<<20)-len(encoded))
	if err := store.Save(op); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(op.ID)
	if err != nil || loaded.Error != op.Error {
		t.Fatalf("boundary-sized snapshot failed to round-trip unchanged: %v", err)
	}
}

func TestSaveRefusesMetadataWithoutAbandonmentHeadroom(t *testing.T) {
	files, store := operationFixture(t)
	op := &app.Operation{Version: 1, ID: "no-headroom", Root: files.Root, Kind: "exec", State: "running", Exec: &app.ExecRequest{Argv: []string{"x"}}}
	encoded, err := json.MarshalIndent(op, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	// One remaining byte cannot hold the transition from running to abandoned.
	op.Exec.Argv[0] = strings.Repeat("x", (2<<20)-len(encoded)-1)
	if err := store.Save(op); err == nil {
		t.Fatal("accepted initial metadata without room to abandon the operation")
	}
	if _, err := store.Load(op.ID); !os.IsNotExist(err) {
		t.Fatalf("refused initial snapshot left a journal behind: %v", err)
	}
}

func TestBoundaryMetadataCanStillBeAbandoned(t *testing.T) {
	files, store := operationFixture(t)
	op := &app.Operation{Version: 1, ID: "can-abandon", Root: files.Root, Kind: "exec", State: "abandoned", Exec: &app.ExecRequest{Argv: []string{"x"}}}
	encoded, err := json.MarshalIndent(op, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	op.Exec.Argv[0] = strings.Repeat("x", (2<<20)-len(encoded))
	op.State = "running"
	if err := store.Save(op); err != nil {
		t.Fatal(err)
	}
	if _, err := app.New(files, nil, store).Abandon(t.Context(), op.ID); err != nil {
		t.Fatalf("accepted boundary metadata cannot be abandoned: %v", err)
	}
	loaded, err := store.Load(op.ID)
	if err != nil || loaded.State != "abandoned" || loaded.Exec.Argv[0] != op.Exec.Argv[0] {
		t.Fatalf("abandonment did not preserve a readable operation: %v", err)
	}
}

func TestListRetainsReadableOperationsAlongsideCorruptJournals(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	files := config.New(root)
	store := New(files)
	for _, id := range []string{"alpha", "zulu"} {
		if err := store.Save(&app.Operation{Version: 1, ID: id, Root: files.Root, Kind: "workspace.add", State: "partial"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"broken", "invalid"} {
		if err := files.Write([]string{".bearforge", "state", "operations", id + ".json"}, []byte("{")); err != nil {
			t.Fatal(err)
		}
	}
	ops, err := store.List()
	if err == nil || !strings.Contains(err.Error(), "broken") || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("journal diagnostics lost: %v", err)
	}
	if len(ops) != 2 || ops[0].ID != "alpha" || ops[1].ID != "zulu" {
		t.Fatalf("readable operations hidden by corrupt journal: %+v", ops)
	}
}

func TestRootLockExcludesOtherWriters(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	files := config.New(root)
	first, second := New(files), New(files)
	release, err := first.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if unlock, err := second.Lock(ctx); err == nil {
		unlock()
		release()
		t.Fatal("concurrent writer acquired lock")
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	unlock, err := second.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
}
