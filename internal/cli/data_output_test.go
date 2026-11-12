package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"

	"bearforge/internal/app"
	"bearforge/internal/domain"
)

func TestRenderDataPreservesNestedFieldsAndNumbers(t *testing.T) {
	data := map[string]any{
		"zeta": "last",
		"alpha": map[string]any{
			"number": int64(9007199254740993),
			"items":  []any{map[string]any{"enabled": false, "missing": nil}, "second"},
			"empty":  map[string]any{},
			"list":   []any{},
		},
	}
	output := dataOutput(t, data)
	for _, want := range []string{"FIELD", "VALUE", "alpha.empty", "(empty object)", "alpha.items[0].enabled", "false", "alpha.items[0].missing", "null", "alpha.items[1]", "second", "alpha.list", "No entries.", "alpha.number", "9007199254740993", "zeta", "last"} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q:\n%s", want, output)
		}
	}
	paths := []string{"alpha.empty", "alpha.items[0].enabled", "alpha.items[0].missing", "alpha.items[1]", "alpha.list", "alpha.number", "zeta"}
	previous := -1
	for _, path := range paths {
		position := strings.Index(output, path)
		if position <= previous {
			t.Fatalf("fields are not sorted: %q", output)
		}
		previous = position
	}
}

func TestRenderDataPreservesAnonymousRetiredEntries(t *testing.T) {
	data := []struct {
		Name    string
		Retired domain.RetiredRepository
	}{{Name: "api", Retired: domain.RetiredRepository{
		Repository: domain.Repository{ID: "repo-id", Remotes: map[string]string{"origin": "https://example.test/repo"}, Base: "main", Provider: map[string]string{"host": "example"}},
		Operation:  "retirement-id", Fingerprint: "fingerprint-value",
	}}}
	output := dataOutput(t, data)
	for _, want := range []string{"[0].Name", "api", "[0].Retired.Repository.ID", "repo-id", "[0].Retired.Repository.Remotes.origin", "https://example.test/repo", "[0].Retired.Repository.Base", "main", "[0].Retired.Repository.Provider.host", "example", "[0].Retired.Operation", "retirement-id", "[0].Retired.Fingerprint", "fingerprint-value"} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q:\n%s", want, output)
		}
	}
}

func TestRenderDataFlatRecordsUseSortedColumns(t *testing.T) {
	id := "0123456789abcdef0123456789abcdef"
	output := dataOutput(t, []any{
		map[string]any{"retirement": id, "name": "api", "count": int64(9007199254740993), "active": false, "optional": nil},
		map[string]any{"name": "worker", "retirement": "another-complete-retirement-id", "active": true},
	})
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if got := strings.Fields(lines[0]); strings.Join(got, " ") != "ACTIVE COUNT NAME OPTIONAL RETIREMENT" {
		t.Fatalf("columns are not sorted uppercase keys: %q", lines[0])
	}
	for _, want := range []string{id, "9007199254740993", "false", "null", "api", "worker", "another-complete-retirement-id", "true"} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "[0]") || strings.Contains(output, "FIELD") {
		t.Fatalf("flat records were rendered as flattened fields:\n%s", output)
	}
	if len(lines) < 3 || !strings.Contains(lines[1], "api") || !strings.Contains(lines[2], "worker") {
		t.Fatalf("record order or row grouping changed:\n%s", output)
	}
}

func TestRenderDataNestedRecordsRetainFlattenedPaths(t *testing.T) {
	output := dataOutput(t, []any{
		map[string]any{"name": "api", "details": map[string]any{"state": "retired"}},
		map[string]any{"name": "worker", "details": []any{"archived"}},
	})
	for _, want := range []string{"FIELD", "VALUE", "[0].name", "api", "[0].details.state", "retired", "[1].name", "worker", "[1].details[0]", "archived"} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q:\n%s", want, output)
		}
	}
}

func TestRenderDataOperationDetailsPreserveProcessOutcomes(t *testing.T) {
	operation := app.Operation{
		Version: 1, ID: "full-operation-id", Root: "/root", Kind: "exec", Workspace: "dev", State: "failed",
		Created: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Steps:   []app.Step{{Repository: "api", Create: false, State: "failed", Execution: &domain.ProcessSummary{ExitCode: 23, Duration: 9007199254740993, Cancelled: true, Signal: "TERM"}}},
		Error:   "process failed",
	}
	for _, data := range []any{operation, &operation} {
		output := dataOutput(t, data)
		for _, want := range []string{"schema_version", "id", "full-operation-id", "root", "/root", "kind", "exec", "workspace", "dev", "state", "failed", "created", "2026-01-02T03:04:05Z", "steps[0].repository", "api", "steps[0].create", "false", "steps[0].state", "steps[0].execution.exit_code", "23", "steps[0].execution.duration_ns", "9007199254740993", "steps[0].execution.cancelled", "true", "steps[0].execution.signal", "TERM", "error", "process failed"} {
			if !strings.Contains(output, want) {
				t.Errorf("%T output missing %q:\n%s", data, want, output)
			}
		}
	}
}

func TestRenderDataWorktreeTable(t *testing.T) {
	output := dataOutput(t, []domain.Worktree{
		{Path: "/repo", Branch: "refs/heads/main", Head: "complete-head", Locked: true, Prunable: true},
		{Path: "/bare", Bare: true},
		{Path: "/detached", Head: "detached-head"},
	})
	for _, want := range []string{"PATH", "BRANCH", "HEAD", "STATE", "/repo", "refs/heads/main", "complete-head", "locked, prunable", "/bare", "bare", "/detached", "detached-head", "detached"} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q:\n%s", want, output)
		}
	}
}

func TestRenderDataOperationListKeepsFullIDs(t *testing.T) {
	id := "0123456789abcdef0123456789abcdef"
	output := dataOutput(t, []app.Operation{{ID: id, Kind: "workspace.create", Workspace: "dev", State: "complete", Created: time.Date(2026, 1, 2, 3, 4, 5, 123, time.UTC)}})
	for _, want := range []string{"ID", "COMMAND", "WORKSPACE", "STATE", "CREATED", id, "workspace.create", "dev", "complete", "2026-01-02T03:04:05.000000123Z"} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q:\n%s", want, output)
		}
	}
}

func TestRenderDataEmptyAndNil(t *testing.T) {
	var operation *app.Operation
	for _, data := range []any{nil, operation} {
		if output := dataOutput(t, data); output != "" {
			t.Errorf("%T: got %q, want no output", data, output)
		}
	}
	for _, data := range []any{[]string{}, []any(nil), []domain.Worktree(nil), []app.Operation{}, [0]string{}} {
		if output := dataOutput(t, data); output != "No entries.\n" {
			t.Errorf("%T: got %q, want empty message", data, output)
		}
	}
}

func TestRenderDataSanitizesStringsAndCells(t *testing.T) {
	unsafe := "hello\x1b[2J\n\r\t\x00world"
	if output := dataOutput(t, unsafe); output != "helloworld\n" {
		t.Fatalf("unsafe string output: %q", output)
	}
	for _, data := range []any{
		map[string]any{unsafe: unsafe},
		[]domain.Worktree{{Path: unsafe, Branch: unsafe, Head: unsafe}},
		[]app.Operation{{ID: unsafe, Kind: unsafe, Workspace: unsafe, State: unsafe}},
	} {
		output := dataOutput(t, data)
		if strings.ContainsAny(output, "\x1b\r\t\x00") || strings.Contains(output, "hello\n") || !strings.Contains(output, "helloworld") {
			t.Errorf("%T unsafe output: %q", data, output)
		}
	}
}

func TestRenderDataReturnsWriteErrors(t *testing.T) {
	failure := errors.New("output unavailable")
	for _, data := range []any{"text", []string{}, map[string]any{"field": "value"}, []domain.Worktree{{Path: "/repo"}}, []app.Operation{{ID: "id"}}} {
		p := &colorprofile.Writer{Forward: dataFailWriter{failure}, Profile: colorprofile.ASCII}
		if err := renderData(data, p); !errors.Is(err, failure) {
			t.Errorf("%T: got error %v, want %v", data, err, failure)
		}
	}
}

func TestRenderDataRejectsUnencodableData(t *testing.T) {
	var output bytes.Buffer
	p := &colorprofile.Writer{Forward: &output, Profile: colorprofile.ASCII}
	if err := renderData(make(chan int), p); err == nil {
		t.Fatal("expected encoding error")
	}
	if output.Len() != 0 {
		t.Fatalf("encoding error emitted partial output: %q", output.String())
	}
}

func dataOutput(t *testing.T, data any) string {
	t.Helper()
	var output bytes.Buffer
	p := &colorprofile.Writer{Forward: &output, Profile: colorprofile.ASCII}
	if err := renderData(data, p); err != nil {
		t.Fatal(err)
	}
	return output.String()
}

type dataFailWriter struct {
	err error
}

func (w dataFailWriter) Write([]byte) (int, error) {
	return 0, w.err
}
