package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"bearforge/internal/app"
)

func TestRepositoryLifecycleCLIPreviewsConfirmsAndRestores(t *testing.T) {
	source, store := dashboardAddFixture(t)
	var out, errOut bytes.Buffer
	base := []string{"--root", store.Root, "--json", "repo", "remove", "api"}
	code := Run(t.Context(), append(append([]string{}, base...), "--dry-run"), strings.NewReader(""), &out, &errOut)
	if code != 0 {
		t.Fatalf("preview: %d %s %s", code, out.String(), errOut.String())
	}
	var preview struct {
		Data app.RepositoryPlan `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &preview); err != nil || preview.Data.Destination == "" {
		t.Fatalf("missing concrete move: %s %v", out.String(), err)
	}
	if _, err := os.Stat(preview.Data.Source); err != nil {
		t.Fatal("preview moved repository")
	}
	out.Reset()
	errOut.Reset()
	if code := Run(t.Context(), base, strings.NewReader(""), &out, &errOut); code != 3 {
		t.Fatalf("confirmation bypass: %d %s", code, out.String())
	}
	out.Reset()
	errOut.Reset()
	code = Run(t.Context(), append(append([]string{}, base...), "--yes"), strings.NewReader(""), &out, &errOut)
	var retired app.Result
	if code != 0 || json.Unmarshal(out.Bytes(), &retired) != nil || retired.OperationID == "" {
		t.Fatalf("retire: %d %s %s", code, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	code = Run(t.Context(), []string{"--root", store.Root, "--json", "repo", "retired"}, strings.NewReader(""), &out, &errOut)
	if code != 0 || !strings.Contains(out.String(), retired.OperationID) {
		t.Fatalf("retired listing hides recovery identity: %d %s", code, out.String())
	}
	out.Reset()
	errOut.Reset()
	code = Run(t.Context(), []string{"--root", store.Root, "--json", "repo", "restore", "api", "--operation", retired.OperationID, "--yes"}, strings.NewReader(""), &out, &errOut)
	if code != 0 {
		t.Fatalf("restore: %d %s %s", code, out.String(), errOut.String())
	}
	c, _, err := source.Store.LoadCatalog()
	if err != nil || len(c.Retired) != 0 || len(c.Repositories) != 2 {
		t.Fatalf("restored catalog: %+v %v", c, err)
	}
}
