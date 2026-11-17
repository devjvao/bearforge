package app_test

import (
	"path/filepath"
	"testing"

	"bearforge/internal/app"
	"bearforge/internal/config"
	"bearforge/internal/domain"
)

func TestImportChoicesRecognizeBitbucketCloudSSHHostAlias(t *testing.T) {
	store := config.New(filepath.Join(t.TempDir(), "code"))
	catalog, revision, err := store.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	catalog.Repositories["existing"] = domain.Repository{ID: "existing", Remotes: map[string]string{"origin": "git@ssh.bitbucket.org:team/api.git"}}
	if err := store.SaveCatalog(catalog, revision); err != nil {
		t.Fatal(err)
	}
	service := app.New(store, nil, nil)
	source := domain.RepositorySource{Provider: "bitbucket", Host: "bitbucket.org", APIURL: "https://api.bitbucket.org/2.0", GitURL: "https://bitbucket.org"}
	remote := domain.RemoteRepository{Provider: source.Provider, Host: source.Host, APIURL: source.APIURL, ID: "repo-id", Namespace: "team", Name: "api", FullName: "team/api", HTTPSURL: "https://bitbucket.org/team/api.git", SSHURL: "git@ssh.bitbucket.org:team/api.git"}
	choices, err := service.RepositoryImportChoices(t.Context(), source, []domain.RemoteRepository{remote})
	if err != nil || len(choices) != 1 || !choices[0].Disabled || choices[0].RegisteredID != "existing" {
		t.Fatalf("Cloud SSH alias allowed a duplicate import: %+v, %v", choices, err)
	}
}
