package cli

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"unicode"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"bearforge/internal/app"
	"bearforge/internal/domain"
)

func TestResultRowsNarrowLayoutsPreserveEveryField(t *testing.T) {
	headers := []string{"NAME", "STATUS", "BRANCH", "CHANGES", "UPSTREAM", "EXIT", "DURATION", "DETAILS", "PATH"}
	rows := [][]string{
		{"demo / api", "changes", "feature (expected main)", "staged=1 modified=2 untracked=3 conflicts=4 detached unborn", "origin/main ahead=2 behind=3 gone", "7", "1.5s (captured output truncated)", "base=main, start=abc123, tracking=same-name, new branch", "/workspaces/" + strings.Repeat("long-unbroken-path", 9) + "/界面/api"},
		{"demo / web", "clean", "main", "staged=0 modified=0 untracked=0 conflicts=0", "origin/main", "0", "2s", "", "/workspaces/web"},
	}
	for _, width := range []int{36, 60, 80} {
		for _, profile := range []colorprofile.Profile{colorprofile.ASCII, colorprofile.TrueColor} {
			t.Run(fmt.Sprintf("width=%d/profile=%v", width, profile), func(t *testing.T) {
				p := &colorprofile.Writer{Profile: profile}
				view := renderResultRows(p, headers, rows, width)
				assertResultLinesFit(t, view, width)
				plain := ansi.Strip(view)
				records := strings.Split(plain, "\n\n")
				if len(records) != len(rows) {
					t.Fatalf("got %d records, want %d: %q", len(records), len(rows), plain)
				}
				for index, record := range records {
					compact := compactResultText(record)
					for column, value := range rows[index] {
						want := compactResultText(headers[column] + ": " + value)
						if !strings.Contains(compact, want) {
							t.Errorf("record %d lost field %q: %q", index, want, record)
						}
					}
				}
				if profile == colorprofile.ASCII && strings.Contains(view, "\x1b") {
					t.Fatalf("plain layout contains escapes: %q", view)
				}
				if profile > colorprofile.ASCII && !strings.Contains(view, "\x1b[") {
					t.Fatalf("colored layout lost styling: %q", view)
				}
			})
		}
	}
}

func TestResultRowsWideLayoutKeepsExistingTable(t *testing.T) {
	headers := []string{"NAME", "STATUS", "BRANCH", "PATH"}
	rows := [][]string{{"demo / api", "changes", "feature", "/workspace/demo/api"}, {"demo / web", "clean", "main", "/workspace/demo/web"}}
	for _, profile := range []colorprofile.Profile{colorprofile.ASCII, colorprofile.TrueColor} {
		t.Run(fmt.Sprint(profile), func(t *testing.T) {
			p := &colorprofile.Writer{Profile: profile}
			got := renderResultRows(p, headers, rows, 160)
			if want := terminalTable(p, headers, rows); got != want {
				t.Fatalf("wide layout changed table:\ngot %q\nwant %q", got, want)
			}
			assertResultLinesFit(t, got, 160)
		})
	}
}

func TestResultRowsSanitizeHeadersAndValuesBeforeLayout(t *testing.T) {
	headers := []string{"NAME\x1b[2J\r", "STATUS\t", "PATH"}
	rows := [][]string{{"api\x1b]0;injected-title\a\n", "clean\x1b[31m", "/workspace/\x00api\r\t" + strings.Repeat("x", 90)}}
	for _, width := range []int{36, 60, 80, 160} {
		for _, profile := range []colorprofile.Profile{colorprofile.ASCII, colorprofile.TrueColor} {
			t.Run(fmt.Sprintf("width=%d/profile=%v", width, profile), func(t *testing.T) {
				view := renderResultRows(&colorprofile.Writer{Profile: profile}, headers, rows, width)
				assertResultLinesFit(t, view, width)
				if strings.Contains(view, "\x1b[2J") || strings.Contains(view, "\x1b]") || strings.Contains(view, "injected-title") {
					t.Fatalf("terminal control injection: %q", view)
				}
				plain := ansi.Strip(view)
				for _, character := range plain {
					if unicode.IsControl(character) && character != '\n' {
						t.Fatalf("unexpected control %U in %q", character, view)
					}
				}
				for _, want := range []string{"NAME", "STATUS", "PATH", "api", "clean", "/workspace/api" + strings.Repeat("x", 90)} {
					if !strings.Contains(compactResultText(plain), want) {
						t.Errorf("sanitization lost %q: %q", want, plain)
					}
				}
			})
		}
	}
}

func TestResultDefaultsToEightyColumnLayout(t *testing.T) {
	var out, errOut bytes.Buffer
	o := &options{out: &out, errOut: &errOut, color: "never"}
	r := app.NewResult("status")
	path := "/workspaces/" + strings.Repeat("repository", 14)
	r.Items = []app.Item{{Repository: "api", Outcome: "changes", Branch: "feature", Path: path, Status: &domain.Status{Staged: 1, Modified: 2, Untracked: 3, Conflicts: 4}}}
	if err := o.renderResult(r); err != nil {
		t.Fatal(err)
	}
	assertResultLinesFit(t, out.String(), 80)
	if !strings.HasPrefix(out.String(), "status  [success]\n\nNAME: api\n") {
		t.Fatalf("heading or stacked layout missing: %q", out.String())
	}
	if !strings.Contains(compactResultText(out.String()), path) || errOut.Len() != 0 {
		t.Fatalf("lost path or unexpected diagnostics: %q %q", out.String(), errOut.String())
	}
}

func TestDataNarrowRecordsPreserveFields(t *testing.T) {
	path := "/workspaces/" + strings.Repeat("unbroken-path", 10)
	id := strings.Repeat("0123456789abcdef", 7)
	cases := []struct {
		name string
		data any
		want []string
	}{
		{
			name: "worktrees",
			data: []domain.Worktree{{Path: path, Branch: "refs/heads/feature", Head: "complete-head", Locked: true, Prunable: true}},
			want: []string{"PATH: " + path, "BRANCH: refs/heads/feature", "HEAD: complete-head", "STATE: locked, prunable"},
		},
		{
			name: "operations",
			data: []app.Operation{{ID: id, Kind: "workspace.create", Workspace: "development", State: "failed"}},
			want: []string{"ID: " + id, "COMMAND: workspace.create", "WORKSPACE: development", "STATE: failed", "CREATED: 0001-01-01T00:00:00Z"},
		},
	}
	for _, test := range cases {
		for _, width := range []int{36, 60, 80} {
			for _, profile := range []colorprofile.Profile{colorprofile.ASCII, colorprofile.TrueColor} {
				t.Run(fmt.Sprintf("%s/width=%d/profile=%v", test.name, width, profile), func(t *testing.T) {
					var out bytes.Buffer
					p := &colorprofile.Writer{Forward: &out, Profile: profile}
					if err := renderData(test.data, p, width); err != nil {
						t.Fatal(err)
					}
					assertResultLinesFit(t, out.String(), width)
					plain := compactResultText(ansi.Strip(out.String()))
					for _, want := range test.want {
						if !strings.Contains(plain, compactResultText(want)) {
							t.Errorf("missing field %q: %q", want, out.String())
						}
					}
				})
			}
		}
		t.Run(test.name+"/result-width", func(t *testing.T) {
			var out, errOut bytes.Buffer
			o := &options{out: &out, errOut: &errOut, color: "never"}
			r := app.NewResult("records.list")
			r.Data = test.data
			if err := o.renderResult(r); err != nil {
				t.Fatal(err)
			}
			assertResultLinesFit(t, out.String(), 80)
			for _, want := range test.want {
				if !strings.Contains(compactResultText(out.String()), compactResultText(want)) {
					t.Errorf("result missing field %q: %q", want, out.String())
				}
			}
		})
	}
}

func assertResultLinesFit(t *testing.T, view string, width int) {
	t.Helper()
	for index, line := range strings.Split(view, "\n") {
		if got := ansi.StringWidth(line); got > width {
			t.Errorf("line %d width %d exceeds %d: %q", index+1, got, width, line)
		}
	}
}

func compactResultText(value string) string {
	return strings.Map(func(character rune) rune {
		if unicode.IsSpace(character) {
			return -1
		}
		return character
	}, value)
}
