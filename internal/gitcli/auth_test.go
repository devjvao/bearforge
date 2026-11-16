package gitcli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestConnectionRoutingIsScopedAndPersistsWithoutChangingOtherHelpers(t *testing.T) {
	client, bare, root := fixture(t)
	remote := "https://git.example.com/scm/team/api.git"
	binding := ConnectionBinding{Connection: "work", GrantID: "grant", URL: remote, Executable: filepath.Join(root, "Bear Forge", "bearforge.exe"), RegistryPath: filepath.Join(root, "auth.json")}
	scoped, err := client.WithConnection(binding)
	if err != nil {
		t.Fatal(err)
	}
	out, err := scoped.Run(t.Context(), bare, "config", "--get-urlmatch", "credential.helper", remote)
	if err != nil || !strings.Contains(string(out), "git-credential") || !strings.Contains(string(out), "--connection") {
		t.Fatalf("invocation routing missing: %s, %v", out, err)
	}
	if _, err := client.Run(t.Context(), bare, "config", "--get-urlmatch", "credential.helper", remote); !isExit(err, 1) {
		t.Fatal("creating scoped client persisted configuration")
	}
	if _, err := client.Run(t.Context(), bare, "config", "--local", "credential.https://unrelated.example.com.helper", "existing-helper"); err != nil {
		t.Fatal(err)
	}
	if err := scoped.BindConnection(t.Context(), bare, binding); err != nil {
		t.Fatal(err)
	}
	other, err := client.Config(t.Context(), bare, "credential.https://unrelated.example.com.helper")
	if err != nil || other != "existing-helper" {
		t.Fatal("unrelated credential helper changed")
	}
	out, err = client.Run(t.Context(), bare, "config", "--get-urlmatch", "credential.helper", remote)
	if err != nil || !strings.Contains(string(out), "git-credential") {
		t.Fatal("ordinary Git cannot reuse selected connection")
	}
	if err := scoped.BindConnection(t.Context(), bare, binding); err != nil {
		t.Fatal("binding is not idempotent:", err)
	}
}

func TestConnectionRoutingRefusesConflictingScopedHelper(t *testing.T) {
	client, bare, root := fixture(t)
	remote := "https://git.example.com/scm/team/api.git"
	key := "credential." + remote + ".helper"
	if _, err := client.Run(t.Context(), bare, "config", "--local", key, "existing-helper"); err != nil {
		t.Fatal(err)
	}
	binding := ConnectionBinding{Connection: "work", GrantID: "grant", URL: remote, Executable: filepath.Join(root, "bearforge.exe")}
	if err := client.BindConnection(t.Context(), bare, binding); err == nil {
		t.Fatal("conflicting helper overwritten without review")
	}
	value, err := client.Config(t.Context(), bare, key)
	if err != nil || value != "existing-helper" {
		t.Fatal("refused association changed the helper")
	}
}

func TestReviewedConnectionRebindingAcceptsOnlyExpectedPriorHelper(t *testing.T) {
	client, bare, root := fixture(t)
	previous := ConnectionBinding{Connection: "work", GrantID: "previous", URL: "https://git.example.com/scm/team/api.git", Executable: filepath.Join(root, "bearforge.exe")}
	current := previous
	current.GrantID = "replacement"
	if err := client.BindConnection(t.Context(), bare, previous); err != nil {
		t.Fatal(err)
	}
	if err := client.BindConnection(t.Context(), bare, current); err == nil {
		t.Fatal("ordinary bind silently replaced a grant")
	}
	if err := client.RebindConnection(t.Context(), bare, previous, current); err != nil {
		t.Fatal("reviewed reassociation failed:", err)
	}
	if err := client.RebindConnection(t.Context(), bare, previous, current); err != nil {
		t.Fatal("reassociation resume is not idempotent:", err)
	}
	key := "credential." + current.URL + ".helper"
	if _, err := client.Run(t.Context(), bare, "config", "--local", "--replace-all", key, "another-helper"); err != nil {
		t.Fatal(err)
	}
	if err := client.RebindConnection(t.Context(), bare, previous, current); err == nil {
		t.Fatal("reviewed reassociation overwrote an externally changed helper")
	}
}
