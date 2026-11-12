package cli

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"

	"bearforge/internal/app"
	"bearforge/internal/domain"
)

func TestResultsCoverCommands(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	commands := []string{"workspace.list", "workspace.create", "workspace.add", "workspace.remove", "workspace.remove-repo", "status", "doctor", "sync", "repo.add", "repo.retire", "repo.restore", "config.validate", "operations.abandon", "exec"}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			var out, errOut bytes.Buffer
			o := &options{out: &out, errOut: &errOut}
			r := app.NewResult(command)
			r.Items = []app.Item{{Repository: "api", Outcome: "clean", Branch: "feature", Path: "/workspace/api"}}
			if err := o.renderResult(r); err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{strings.ReplaceAll(command, ".", " "), "[success]", "NAME", "STATUS", "BRANCH", "PATH", "api", "feature", "/workspace/api"} {
				if !strings.Contains(out.String(), want) {
					t.Fatalf("missing %q in %q", want, out.String())
				}
			}
			if strings.Contains(out.String(), "\x1b") || errOut.Len() != 0 {
				t.Fatalf("unexpected diagnostics or styling: %q %q", out.String(), errOut.String())
			}
		})
	}
}

func TestResultStatusIncludesHealthAndPlanDetails(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var out, errOut bytes.Buffer
	o := &options{out: &out, errOut: &errOut}
	ahead, behind := 2, 3
	r := app.NewResult("status")
	r.Items = []app.Item{{Repository: "api", Outcome: "changes", Branch: "actual", ExpectedBranch: "expected", Base: "main", Start: "abc123", Tracking: "same-name", CreateBranch: true, Status: &domain.Status{Staged: 1, Modified: 2, Untracked: 3, Conflicts: 4, Detached: true, Unborn: true, Upstream: "origin/main", Ahead: &ahead, Behind: &behind, UpstreamGone: true}}}
	if err := o.renderResult(r); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"expected expected", "staged=1", "modified=2", "untracked=3", "conflicts=4", "detached", "unborn", "origin/main", "ahead=2", "behind=3", "gone", "base=main", "start=abc123", "tracking=same-name", "new branch"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in %q", want, out.String())
		}
	}
}

func TestResultFailuresKeepRecoveryAndStderr(t *testing.T) {
	var out, errOut bytes.Buffer
	o := &options{out: &out, errOut: &errOut, activeRoot: "/storage root"}
	r := app.NewResult("exec")
	r.Outcome, r.OperationID = "partial", "operation-123"
	r.Items = []app.Item{{Repository: "api", Outcome: "failed", Problem: &app.Problem{Message: "child failed"}, Execution: &domain.ProcessResult{ExitCode: 7, StdoutTruncated: true}}}
	r.Problem = &app.Problem{Message: "execution failed"}
	if err := o.renderResult(r); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"EXIT", "DURATION", "7", "captured output truncated", "operation-123"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in %q", want, out.String())
		}
	}
	for _, want := range []string{"child failed", "Error: execution failed", "Inspect execution with:", "--root", "operation-123", "commands are never replayed"} {
		if !strings.Contains(errOut.String(), want) {
			t.Fatalf("missing %q in %q", want, errOut.String())
		}
	}
	if strings.Contains(out.String(), "child failed") || strings.Contains(errOut.String(), "Resume with:") {
		t.Fatal("diagnostics or execution recovery changed streams/meaning")
	}
}

func TestResultEmptyStatesAndControlSanitization(t *testing.T) {
	var out, errOut bytes.Buffer
	o := &options{out: &out, errOut: &errOut, color: "never"}
	for _, command := range []string{"workspace.list", "config.validate", "operations.abandon", "sync"} {
		out.Reset()
		if err := o.renderResult(app.NewResult(command)); err != nil {
			t.Fatal(err)
		}
		if len(strings.Split(strings.TrimSpace(out.String()), "\n")) < 2 {
			t.Fatalf("silent empty result: %q", out.String())
		}
	}
	out.Reset()
	r := app.NewResult("workspace.list")
	r.Items = []app.Item{{Workspace: "demo\x1b[2J\n", Outcome: "registered"}}
	if err := o.renderResult(r); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "\x1b") || strings.Contains(out.String(), "demo\n") {
		t.Fatalf("unsafe terminal text: %q", out.String())
	}
}

func TestResultFailureNeverReportsValidationSuccess(t *testing.T) {
	var out, errOut bytes.Buffer
	o := &options{out: &out, errOut: &errOut}
	r := app.NewResult("config.validate")
	r.Outcome = "failed"
	r.Problem = &app.Problem{Message: "invalid catalog"}
	if err := o.renderResult(r); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Configuration is valid") || !strings.Contains(errOut.String(), "invalid catalog") {
		t.Fatalf("misleading failure: %q %q", out.String(), errOut.String())
	}
	out.Reset()
	r = app.NewResult("operations.abandon")
	r.Outcome = "planned"
	if err := o.renderResult(r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Would stop") || strings.Contains(out.String(), "tracking stopped") {
		t.Fatalf("preview claimed mutation: %q", out.String())
	}
}

func TestResultPropagatesWriteFailure(t *testing.T) {
	reader, writer := io.Pipe()
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := writer.Close(); err != nil {
			t.Error(err)
		}
	})
	o := &options{out: writer}
	if err := o.renderResult(app.NewResult("config.validate")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write error lost: %v", err)
	}
}

func TestPresentationDisablesControlsForPipesAndNoColor(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "")
	var out bytes.Buffer
	o := &options{out: &out, color: "always"}
	if o.presentation(&out, false).Profile != colorprofile.ASCII {
		t.Fatal("forced color leaked into pipe")
	}
	t.Setenv("NO_COLOR", "please")
	if o.presentation(&out, true).Profile != colorprofile.ASCII {
		t.Fatal("NO_COLOR did not override forced color")
	}
}
