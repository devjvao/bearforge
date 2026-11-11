package operation

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bearforge/internal/app"
	"bearforge/internal/config"
)

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
