//go:build darwin || linux

package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/devjvao/bearforge/internal/app"
)

func TestWorkspaceUpdateConfirmationReviewsAllSelectionsOnce(t *testing.T) {
	master, terminal, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer terminal.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := master.Write([]byte("y\n")); err != nil {
		t.Fatal(err)
	}
	var review bytes.Buffer
	o := options{in: terminal, errOut: &review, output: "human"}
	plan := app.NewResult("workspace.update")
	plan.Data = app.WorkspaceUpdatePlan{Workspace: "release", Rows: []app.WorkspaceUpdateRow{
		{Repository: "api", Branch: "main", Status: "ready", Detail: "old-api to new-api", Eligible: true},
		{Repository: "web", Branch: "main", Status: "ready", Detail: "old-web to new-web", Eligible: true},
		{Repository: "store", Status: "blocked", Detail: "uncommitted changes"},
	}}
	if err := o.confirm(ctx, plan); err != nil {
		t.Fatal(err)
	}
	got := review.String()
	for _, detail := range []string{"api  [selected]", "old-api to new-api", "web  [selected]", "old-web to new-web", "store  [blocked]", "uncommitted changes"} {
		if !strings.Contains(got, detail) {
			t.Fatalf("review missing %q: %s", detail, got)
		}
	}
	if strings.Count(got, "Proceed?") != 1 || !strings.HasSuffix(got, "Proceed? [y/N] ") {
		t.Fatalf("expected a single final confirmation: %s", got)
	}
}
