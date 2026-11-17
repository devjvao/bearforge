package app_test

import (
	"encoding/json"
	"maps"
	"testing"

	"bearforge/internal/app"
)

func TestImportReassociationPreviewDescribesBindingsAndGitScope(t *testing.T) {
	for _, transport := range []string{"https", "ssh"} {
		t.Run(transport, func(t *testing.T) {
			s, store, _ := fixture(t)
			resolver := &importResolver{grant: "old-grant"}
			s.Credentials = resolver
			selected := importSelection("api")
			selected.Source.Connection, selected.Source.GrantID = "work", "old-grant"
			selected.Transport = transport
			preview, err := s.ImportRepositories(t.Context(), app.ImportRequest{Repositories: []app.ImportRepository{selected}, DryRun: true})
			if err != nil {
				t.Fatal(err)
			}
			steps := preview.Data.(app.ImportPlan).Steps
			registered := *steps[0].ImportedRepository
			registered.Provider = maps.Clone(registered.Provider)
			registered.Provider["grant_id"] = "catalog-grant"
			steps[0].PreviousImportedRepository = &registered
			catalog, revision, err := store.LoadCatalog()
			if err != nil {
				t.Fatal(err)
			}
			catalog.Repositories["api"] = registered
			if err := store.SaveCatalog(catalog, revision); err != nil {
				t.Fatal(err)
			}
			op := &app.Operation{Version: 1, ID: "pending-import", Root: store.RootPath(), Kind: "repo.import", State: "partial", Steps: steps}
			if err := s.Journal.Save(op); err != nil {
				t.Fatal(err)
			}
			resolver.grant = "new-grant"
			result, err := s.ReassociateImport(t.Context(), app.ImportReassociationRequest{OperationID: op.ID, Connection: "replacement", GrantID: "new-grant", DryRun: true})
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			var payload struct {
				Data struct {
					Changes []struct {
						Origin         string `json:"origin"`
						HelperScope    string `json:"helper_scope"`
						GitIntegration string `json:"git_integration"`
						Previous       struct {
							Connection string `json:"connection"`
							GrantID    string `json:"grant_id"`
						} `json:"previous"`
						Replacement struct {
							Connection string `json:"connection"`
							GrantID    string `json:"grant_id"`
						} `json:"replacement"`
						CatalogPrevious struct {
							Connection string `json:"connection"`
							GrantID    string `json:"grant_id"`
						} `json:"catalog_previous"`
					} `json:"changes"`
				} `json:"data"`
			}
			if err := json.Unmarshal(encoded, &payload); err != nil {
				t.Fatal(err)
			}
			if len(payload.Data.Changes) != 1 {
				t.Fatalf("missing reviewable binding changes: %s", encoded)
			}
			change := payload.Data.Changes[0]
			if change.Previous.Connection != "work" || change.Previous.GrantID != "old-grant" || change.Replacement.Connection != "replacement" || change.Replacement.GrantID != "new-grant" {
				t.Fatalf("missing old/new identity: %s", encoded)
			}
			if change.CatalogPrevious.Connection != "work" || change.CatalogPrevious.GrantID != "catalog-grant" {
				t.Fatalf("missing current catalog binding after an interrupted prior repair: %s", encoded)
			}
			if change.Origin != steps[0].ImportedRepository.Remotes["origin"] {
				t.Fatalf("missing exact origin: %s", encoded)
			}
			if transport == "https" && (change.HelperScope != change.Origin || change.GitIntegration != "replace_scoped_helper") {
				t.Fatalf("missing HTTPS helper scope: %s", encoded)
			}
			if transport == "ssh" && (change.HelperScope != "" || change.GitIntegration != "unchanged_ssh") {
				t.Fatalf("SSH preview proposed Git credential changes: %s", encoded)
			}
			unchanged, err := s.Journal.Load(op.ID)
			if err != nil || unchanged.Steps[0].ImportedRepository.Provider["grant_id"] != "old-grant" {
				t.Fatalf("preview changed journal: %+v %v", unchanged, err)
			}
		})
	}
}
