package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/domain"
)

func TestHumanOperationOutputOrganizesStepsWithoutJournalInternals(t *testing.T) {
	operation := &app.Operation{ID: "pending-import", Root: "D:/team's code", Kind: "repo.import", State: "partial", Error: "transfer interrupted", Steps: []app.Step{
		{Repository: "api", State: "succeeded", RefFormat: "files"},
		{Repository: "web", State: "failed", RefFormat: "reftable", Error: "read access unavailable", ImportedRepository: &domain.Repository{Provider: map[string]string{"grant_id": "opaque-internal-grant"}}},
	}}
	result := app.NewResult("operations.show")
	result.Data = operation
	var output bytes.Buffer
	o := &options{out: &output, errOut: &output, output: "human", color: "never"}
	if err := o.render(result); err != nil {
		t.Fatal(err)
	}
	view := output.String()
	for _, want := range []string{"Operation", "pending-import", "repo.import", "partial", "Steps", "REPOSITORY", "STATE", "api", "succeeded", "web", "failed", "reftable", "read access unavailable", "operations resume pending-import", "operations abandon pending-import --yes"} {
		if !strings.Contains(compactResultText(view), compactResultText(want)) {
			t.Errorf("organized operation output lost %q: %s", want, view)
		}
	}
	for _, internal := range []string{"imported_repository", "grant_id", "opaque-internal-grant", `"steps"`} {
		if strings.Contains(view, internal) {
			t.Errorf("human output exposed journal internals %q: %s", internal, view)
		}
	}
}

func TestOperationAndRepairLayoutsPreserveAllRowsAtEveryWidth(t *testing.T) {
	operation := app.Operation{ID: "operation-" + strings.Repeat("x", 60), Root: "D:/" + strings.Repeat("long-directory/", 12), Kind: "exec", State: "partial", Error: "failure\x1b[2J\u202e details"}
	for index := range 25 {
		operation.Steps = append(operation.Steps, app.Step{Repository: fmt.Sprintf("repo-%02d", index), State: "cancelled", Execution: &domain.ProcessSummary{ExitCode: 23, Duration: time.Second, Cancelled: true, Signal: "TERM"}})
	}
	result := app.NewResult("operations.show")
	result.Data = &operation
	plan := repairPreviewResult().Data.(app.ImportReassociationPlan)
	plan.Changes[0].Replacement.GrantID = strings.Repeat("complete-grant-", 10)
	for _, width := range []int{36, 80, 160} {
		for _, profile := range []colorprofile.Profile{colorprofile.ASCII, colorprofile.TrueColor} {
			t.Run(fmt.Sprintf("width=%d/profile=%v", width, profile), func(t *testing.T) {
				p := &colorprofile.Writer{Profile: profile}
				operationView := operationResultDocument(p, result, width)
				repairView := importReassociationDocument(p, plan, width)
				assertResultLinesFit(t, operationView, width)
				assertResultLinesFit(t, repairView, width)
				plain := ansi.Strip(operationView)
				compact := compactResultText(plain)
				for _, want := range []string{operation.ID, operation.Root, "repo-00", "repo-24", "cancelled", "23", "TERM", "Commands are never rerun", "operations abandon"} {
					if !strings.Contains(compact, compactResultText(want)) {
						t.Errorf("operation lost %q: %s", want, plain)
					}
				}
				if strings.Contains(compact, "operationsresume") || strings.ContainsAny(plain, "\x1b\u202e") {
					t.Fatalf("unsafe execution recovery or terminal control: %q", operationView)
				}
				if !strings.Contains(compactResultText(ansi.Strip(repairView)), plan.Changes[0].Replacement.GrantID) {
					t.Fatalf("repair shortened the replacement grant identity: %q", ansi.Strip(repairView))
				}
			})
		}
	}
}

func TestOperationOutputMachineFormatsRetainFullJournal(t *testing.T) {
	for _, format := range []string{"json", "jsonl"} {
		t.Run(format, func(t *testing.T) {
			result := app.NewResult("operations.show")
			result.Data = &app.Operation{ID: "pending", Kind: "repo.import", State: "partial", Steps: []app.Step{{Repository: "api", ImportedRepository: &domain.Repository{Provider: map[string]string{"grant_id": "retained-grant"}}}}}
			var output bytes.Buffer
			o := &options{out: &output, output: format}
			if err := o.render(result); err != nil {
				t.Fatal(err)
			}
			var decoded struct {
				Data app.Operation `json:"data"`
				Type string        `json:"type"`
			}
			if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
				t.Fatal(err)
			}
			if len(decoded.Data.Steps) != 1 || decoded.Data.Steps[0].ImportedRepository.Provider["grant_id"] != "retained-grant" || (format == "jsonl" && decoded.Type != "result") {
				t.Fatalf("machine journal contract changed: %s", output.String())
			}
		})
	}
}

func TestOperationListShowsAllEntriesAndFinishedState(t *testing.T) {
	p := &colorprofile.Writer{Profile: colorprofile.ASCII}
	result := app.NewResult("operations.list")
	result.Data = []app.Operation{}
	if view := operationResultDocument(p, result, 80); !strings.Contains(view, "No recorded operations") {
		t.Fatalf("missing empty state: %s", view)
	}
	operations := []app.Operation{}
	for index := range 25 {
		operations = append(operations, app.Operation{ID: fmt.Sprintf("operation-%02d", index), Kind: "repo.import", State: "complete", Root: "D:/code"})
	}
	result.Data = operations
	view := operationResultDocument(p, result, 80)
	for _, want := range []string{"25 recorded", "0 unfinished", "operation-00", "operation-24", "--root 'D:/code'"} {
		if !strings.Contains(view, want) {
			t.Errorf("operation list lost %q: %s", want, view)
		}
	}
	result.Data = &app.Operation{ID: "finished", State: "complete"}
	if view := operationResultDocument(p, result, 80); strings.Contains(view, "operations resume") || strings.Contains(view, "operations abandon") {
		t.Fatalf("finished operation offers recovery: %s", view)
	}
}

func TestLegacyRepositoryOperationPreservesRemoteAtNarrowWidths(t *testing.T) {
	remote := "https://git.example.com/teams/" + strings.Repeat("long-project/", 8) + "api.git"
	result := app.NewResult("operations.show")
	result.Data = &app.Operation{ID: "legacy-clone", Root: "D:/code", Kind: "repo.add", State: "partial", Remote: remote}
	for _, width := range []int{36, 80} {
		view := operationResultDocument(&colorprofile.Writer{Profile: colorprofile.ASCII}, result, width)
		assertResultLinesFit(t, view, width)
		if !strings.Contains(view, "Remote") || !strings.Contains(compactResultText(view), compactResultText(remote)) {
			t.Errorf("width %d lost legacy remote identity: %s", width, view)
		}
	}
}

func TestHumanRepairPreviewWrapsBindingsAndPreservesReviewDetails(t *testing.T) {
	result := repairPreviewResult()
	plan := result.Data.(app.ImportReassociationPlan)
	change := &plan.Changes[0]
	change.Path = "D:/" + strings.Repeat("repository-location/", 8) + "api.git"
	change.Previous.GrantID = "old-" + strings.Repeat("a", 70)
	change.Replacement.GrantID = "new-" + strings.Repeat("b", 70)
	change.CatalogPrevious = &app.ImportConnectionBinding{Connection: "catalog account", GrantID: "catalog-grant"}
	change.ReviewedBindings = []app.ImportConnectionBinding{{Connection: "previous helper", GrantID: "helper-grant"}}
	result.Data = plan
	var output bytes.Buffer
	o := &options{out: &output, errOut: &output, output: "human", color: "never"}
	if err := o.render(result); err != nil {
		t.Fatal(err)
	}
	assertResultLinesFit(t, output.String(), 80)
	compact := compactResultText(output.String())
	for _, want := range []string{change.Path, change.Previous.GrantID, change.Replacement.GrantID, "catalog account", "catalog-grant", "previous helper", "helper-grant", change.Origin, "useHttpPath=true", "octocat"} {
		if !strings.Contains(compact, compactResultText(want)) {
			t.Errorf("repair preview lost %q: %s", want, output.String())
		}
	}
}
