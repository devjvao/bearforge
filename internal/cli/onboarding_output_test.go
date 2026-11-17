package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/domain"
)

func TestNewRepositoryHumanOutputKeepsRefFormatWhenPiped(t *testing.T) {
	for _, command := range []string{"repo.add", "repo.inspect", "operations.resume"} {
		t.Run(command, func(t *testing.T) {
			result := app.NewResult(command)
			result.Items = []app.Item{{Repository: "api", Outcome: "registered", Path: "/code/repos/api.git", RefFormat: "reftable"}}
			if command == "repo.inspect" {
				result.Data = []domain.Worktree{{Path: "/code/feature/api", Branch: "feature"}}
			}
			var output, diagnostics bytes.Buffer
			o := &options{out: &output, errOut: &diagnostics, color: "always"}
			if err := o.render(result); err != nil {
				t.Fatal(err)
			}
			view := output.String()
			if !strings.Contains(view, "ref-format=reftable") || strings.Contains(view, "\x1b") || strings.Contains(view, `"Path"`) {
				t.Fatalf("new repository data lost structured plain presentation: %s", view)
			}
			assertResultLinesFit(t, view, 80)
		})
	}
}
