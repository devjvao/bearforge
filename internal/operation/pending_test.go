package operation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/devjvao/bearforge/internal/app"
)

func TestPendingSummariesTracksCanonicalContent(t *testing.T) {
	files, writer := operationFixture(t)
	for id, state := range map[string]string{"a-1": "partial", "a": "running", "done": "complete", "old": "abandoned"} {
		op := &app.Operation{Version: 1, Root: files.Root, Kind: "workspace.add", Workspace: id, ID: id, State: state}
		if err := writer.Save(op); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(files.Root, ".bearforge", "state", "operations")
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte(`{"unknown":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	s := New(files) // Existing journals are discovered without a writer populating the cache.
	want := []app.OperationSummary{
		{ID: "a", Kind: "workspace.add", Workspace: "a", State: "running"},
		{ID: "a-1", Kind: "workspace.add", Workspace: "a-1", State: "partial"},
	}
	check := func(want []app.OperationSummary, cached int) {
		t.Helper()
		got, err := s.PendingSummaries(t.Context())
		if err == nil || !strings.Contains(err.Error(), "bad.json") || !reflect.DeepEqual(got, want) {
			t.Fatalf("pending: %+v, %v", got, err)
		}
		if n := len(s.terminal.Load().byID); n != cached {
			t.Fatalf("cached %d terminal journals, want %d", n, cached)
		}
	}
	check(want, 2)
	check(want, 2)

	path := filepath.Join(dir, "done.json")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	document, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	modified := bytes.Replace(document, []byte(`"complete"`), []byte(`"running" `), 1)
	if err := os.WriteFile(path, modified, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "old.json")); err != nil {
		t.Fatal(err)
	}
	check(append(want, app.OperationSummary{ID: "done", Kind: "workspace.add", Workspace: "done", State: "running"}), 0)

	if err := os.WriteFile(path, document, 0600); err != nil {
		t.Fatal(err)
	}
	check(want, 1)
	invalid := bytes.Replace(document, []byte(`"schema_version": 1`), []byte(`"schema_version": 2`), 1)
	if err := os.WriteFile(path, invalid, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := s.PendingSummaries(t.Context())
	if !reflect.DeepEqual(got, want) || err == nil || !strings.Contains(err.Error(), "done.json") || !strings.Contains(err.Error(), "bad.json") {
		t.Fatalf("corrupt replacement: %+v, %v", got, err)
	}
}

func TestTerminalCacheCannotMaskAnOversizedDocument(t *testing.T) {
	files, s := operationFixture(t)
	op := &app.Operation{Version: 1, ID: "done", Root: files.Root, Kind: "exec", State: "complete"}
	if err := s.Save(op); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PendingSummaries(t.Context()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(files.Root, ".bearforge", "state", "operations", "done.json")
	doc, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc = append(doc, bytes.Repeat([]byte(" "), maxDocument-len(doc))...)
	if err := os.WriteFile(path, append(doc, '!'), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PendingSummaries(t.Context()); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("accepted oversized journal: %v", err)
	}
	if _, err := s.Load("done"); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("authoritative load accepted oversized journal: %v", err)
	}
}

func TestTerminalCacheIsScopedToValidatedRoot(t *testing.T) {
	files, s := operationFixture(t)
	other, _ := operationFixture(t)
	op := &app.Operation{Version: 1, ID: "same-id", Root: files.Root, Kind: "exec", State: "complete"}
	if err := s.Save(op); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PendingSummaries(t.Context()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(files.Root, ".bearforge", "state", "operations", "same-id.json")
	doc, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Write([]string{".bearforge", "state", "operations", "same-id.json"}, doc); err != nil {
		t.Fatal(err)
	}
	files.Root = other.Root
	if _, err := s.PendingSummaries(t.Context()); err == nil || !strings.Contains(err.Error(), "schema/root mismatch") {
		t.Fatalf("same ID and contents accepted at a different root: %v", err)
	}
}

func TestTerminalCacheRetainsBoundedStableSubset(t *testing.T) {
	files, s := operationFixture(t)
	dir := filepath.Join(files.Root, ".bearforge", "state", "operations")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(id, state string) {
		t.Helper()
		b, err := json.Marshal(app.Operation{Version: 1, Root: files.Root, ID: id, Kind: "exec", State: state})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, id+".json"), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for i := range maxTerminalRevisions + 1 {
		write(fmt.Sprintf("history-%04d", i), "complete")
	}
	check := func() {
		t.Helper()
		if ops, err := s.PendingSummaries(t.Context()); err != nil || len(ops) != 0 {
			t.Fatalf("unexpected results: %+v, %v", ops, err)
		}
		if n := len(s.terminal.Load().byID); n != maxTerminalRevisions {
			t.Fatalf("cached %d entries, want %d", n, maxTerminalRevisions)
		}
	}
	check()
	write("earlier", "abandoned")
	check()
	last := fmt.Sprintf("history-%04d", maxTerminalRevisions-1)
	if _, ok := s.terminal.Load().byID[last]; ok {
		t.Fatalf("entry %s was not evicted", last)
	}
	write(last, "partial")
	got, err := s.PendingSummaries(t.Context())
	if err != nil || len(got) != 1 || got[0].ID != last {
		t.Fatalf("changed evicted entry: %+v, %v", got, err)
	}
}

func TestConcurrentPendingScansAndReplacement(t *testing.T) {
	files, s := operationFixture(t)
	op := &app.Operation{Version: 1, Root: files.Root, ID: "task", Kind: "exec", State: "complete"}
	if err := s.Save(op); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PendingSummaries(t.Context()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	// Windows can transiently deny either reading or replacing an open file.
	sharingViolation := func(err error) bool {
		return runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(32)) // ERROR_SHARING_VIOLATION
	}
	for range 4 {
		wg.Go(func() {
			for range 12 {
				got, err := s.PendingSummaries(t.Context())
				if len(got) == 0 && sharingViolation(err) {
					continue
				}
				if err != nil || len(got) > 1 || (len(got) == 1 && (got[0].ID != "task" || got[0].State != "partial")) {
					t.Errorf("read across replacement: %+v, %v", got, err)
					return
				}
			}
		})
	}
	op.State = "partial"
	err := s.Save(op)
	wg.Wait()
	if err != nil && !sharingViolation(err) && !(runtime.GOOS == "windows" && errors.Is(err, os.ErrPermission)) {
		t.Fatal(err)
	}
	if err != nil {
		// Windows may refuse a replacement while another reader has the
		// destination open. Retry the writer after the readers have closed.
		if err := s.Save(op); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.PendingSummaries(t.Context())
	if err != nil || len(got) != 1 || got[0].State != "partial" {
		t.Fatalf("concurrent scan hid replacement: %+v, %v", got, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.PendingSummaries(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled scan: %v", err)
	}
}
