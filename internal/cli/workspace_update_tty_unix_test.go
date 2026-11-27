//go:build darwin || linux

package cli

import (
	"bufio"
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/devjvao/bearforge/internal/app"
)

func TestWorkspaceUpdateTerminalPreservesLongFetchCommand(t *testing.T) {
	master, terminal, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer terminal.Close()
	if err := pty.Setsize(terminal, &pty.Winsize{Rows: 12, Cols: 36}); err != nil {
		t.Fatal(err)
	}
	command := "git fetch --no-tags --refmap= -- origin 'refs/heads/" + strings.Repeat("long-ref/", 12) + "feature'"
	plan := app.WorkspaceUpdatePlan{Rows: []app.WorkspaceUpdateRow{{Repository: "api", Status: "sync_required", Detail: command}}}
	if err := writeWorkspaceUpdate(terminal, plan, nil, true); err != nil {
		t.Fatal(err)
	}
	if _, err := terminal.Write([]byte{0}); err != nil {
		t.Fatal(err)
	}
	got, err := bufio.NewReader(master).ReadString(0)
	if err != nil {
		t.Fatal(err)
	}
	want := "api  [sync_required]\n  " + command + "\n\x00"
	if strings.ReplaceAll(got, "\r\n", "\n") != want {
		t.Fatalf("fetch command was hard-wrapped: %q", got)
	}
}

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
