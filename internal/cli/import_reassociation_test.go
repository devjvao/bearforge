package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"bearforge/internal/app"
	"bearforge/internal/auth"
	"bearforge/internal/config"
	"bearforge/internal/domain"
	"bearforge/internal/operation"
)

func repairPreviewResult() app.Result {
	r := app.NewResult("operations.reassociate-import")
	r.Outcome, r.OperationID = "planned", "partial-import"
	r.Data = app.ImportReassociationPlan{OperationID: r.OperationID, Changes: []app.ImportReassociationChange{{
		Repository: "api", Path: "/code/repos/api.git", Origin: "https://github.com/team/api.git",
		Previous:    app.ImportConnectionBinding{Connection: "work", GrantID: "old-grant"},
		Replacement: app.ImportConnectionBinding{Connection: "work", GrantID: "new-grant", Account: "octocat", IdentityVerified: true},
		HelperScope: "https://github.com/team/api.git", GitIntegration: "replace_scoped_helper",
	}}}
	return r
}

func TestImportReassociationHumanReviewExplainsAccountAndGitChanges(t *testing.T) {
	var output bytes.Buffer
	o := &options{out: &output, errOut: &output, output: "human"}
	if err := o.render(repairPreviewResult()); err != nil {
		t.Fatal(err)
	}
	compact := compactResultText(output.String())
	for _, want := range []string{"Connection changes", "Journal work old-grant", "Replacement work new-grant", "Account octocat (verified)", "Origin https://github.com/team/api.git", "Replace Bearforge credential routing", "Scope https://github.com/team/api.git", "useHttpPath=true"} {
		if !strings.Contains(compact, compactResultText(want)) {
			t.Fatalf("review missing %q: %s", want, output.String())
		}
	}
}

func TestImportReassociationJSONRetainsStructuredReview(t *testing.T) {
	var output bytes.Buffer
	o := &options{out: &output, output: "json"}
	if err := o.render(repairPreviewResult()); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Data app.ImportReassociationPlan `json:"data"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil || len(result.Data.Changes) != 1 || result.Data.Changes[0].Replacement.GrantID != "new-grant" {
		t.Fatalf("structured review lost: %s, %v", output.String(), err)
	}
}

func TestImportResumeDryRunIncludesReplacementAccountAndRetainsOldGrant(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" || r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"id":17,"login":"octocat"}`)
	}))
	defer server.Close()
	base := t.TempDir()
	manager := auth.NewManager(auth.Options{RegistryPath: filepath.Join(base, "auth.json"), Secrets: authTestSecrets{}, HTTPClient: server.Client()})
	if _, err := authCommand(t, manager, "test-token\n", "--json", "--no-input", "auth", "login", "--provider", "github", "--host", server.URL, "--api-url", server.URL, "--connection", "work", "--method", "token", "--token-stdin"); err != nil {
		t.Fatal(err)
	}
	connection, err := manager.Connection(t.Context(), "work")
	if err != nil {
		t.Fatal(err)
	}
	managed := filepath.Join(base, "code")
	store := config.New(managed)
	journal := operation.New(store)
	repo := domain.Repository{ID: "api", Remotes: map[string]string{"origin": server.URL + "/team/api.git"}, Provider: map[string]string{
		"kind": "github", "host": strings.TrimPrefix(server.URL, "https://"), "api_url": server.URL,
		"repository": "team/api", "remote_id": "17", "transport": "https", "git_url": server.URL,
		"connection": "work", "grant_id": "replaced-grant",
	}}
	op := &app.Operation{Version: 1, ID: "pending-import", Root: store.RootPath(), Kind: "repo.import", State: "partial", Steps: []app.Step{{Repository: "api", ImportedRepository: &repo, State: "planned"}}}
	if err := journal.Save(op); err != nil {
		t.Fatal(err)
	}
	output, err := authCommand(t, manager, "", "--root", managed, "--json", "--dry-run", "operations", "resume", op.ID, "--connection", "work")
	if err != nil {
		t.Fatalf("dry-run failed: %s %v", output, err)
	}
	var result struct {
		Data app.ImportReassociationPlan `json:"data"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil || len(result.Data.Changes) != 1 {
		t.Fatalf("missing plan: %s %v", output, err)
	}
	change := result.Data.Changes[0]
	if change.Previous.GrantID != "replaced-grant" || change.Replacement.GrantID != connection.GrantID || change.Replacement.Account != "octocat" || !change.Replacement.IdentityVerified {
		t.Fatalf("missing account transition: %+v", change)
	}
	retained, err := journal.Load(op.ID)
	if err != nil || retained.Steps[0].ImportedRepository.Provider["grant_id"] != "replaced-grant" {
		t.Fatalf("dry-run changed saved binding: %+v %v", retained, err)
	}
}

func TestImportReassociationHumanReviewSanitizesAccountAndDescribesSSH(t *testing.T) {
	result := repairPreviewResult()
	plan := result.Data.(app.ImportReassociationPlan)
	plan.Changes[0].Replacement.Account = "octocat\x1b[2J\u202e"
	plan.Changes[0].HelperScope, plan.Changes[0].GitIntegration = "", "unchanged_ssh"
	result.Data = plan
	var output bytes.Buffer
	o := &options{out: &output, output: "human"}
	if err := o.render(result); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(output.String(), "\x1b\u202e") || !strings.Contains(output.String(), "SSH uses its existing authentication") || strings.Contains(output.String(), "useHttpPath") {
		t.Fatalf("unsafe or misleading SSH review: %q", output.String())
	}
}
