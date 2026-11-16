package gitcli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestConnectionHelperFailureStopsLaterAccount(t *testing.T) {
	for _, failing := range []bool{false, true} {
		t.Run(fmt.Sprint(failing), func(t *testing.T) {
			root := t.TempDir()
			executable := filepath.Join(root, "Bear's Forge", "helper")
			if failing {
				if err := os.MkdirAll(filepath.Dir(executable), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 42\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			remote := "https://git.example.com/team/api.git"
			key, helper, err := connectionHelper(ConnectionBinding{Connection: "work's account", GrantID: "grant", URL: remote, Executable: executable})
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), "git", "-c", key+".helper=", "-c", key+".helper="+helper, "-c", key+".helper=!f() { printf 'username=wrong-account\\npassword=wrong-secret\\n'; }; f", "credential", "fill")
			cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
			cmd.Stdin = strings.NewReader("url=" + remote + "\n\n")
			var output, diagnostics bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &diagnostics
			err = cmd.Run()
			if err == nil || strings.Contains(output.String(), "wrong-account") {
				t.Fatalf("helper failure selected another account: %s, %v", output.String(), err)
			}
			if !strings.Contains(diagnostics.String(), "reinstall") {
				t.Fatalf("missing repair diagnostic: %s", diagnostics.String())
			}
		})
	}
}

func TestConnectionHelperPreservesQuotedMetadataAndSecretProtocol(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "Bear's Forge", "helper")
	if err := os.MkdirAll(filepath.Dir(executable), 0700); err != nil {
		t.Fatal(err)
	}
	// The helper accepts only routing metadata and the Git verb. The fake secret
	// is produced inside the helper, never placed in the generated command argv.
	script := "#!/bin/sh\n[ \"$1\" = auth ] && [ \"$2\" = git-credential ] && [ \"$4\" = \"work's account\" ] && [ \"$6\" = grant ] && [ \"${11}\" = get ] && [ \"$#\" = 11 ] || exit 42\nprintf 'username=selected\\npassword=helper-only-secret\\n\\n'\n"
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	remote := "https://git.example.com/team/api.git"
	key, helper, err := connectionHelper(ConnectionBinding{Connection: "work's account", GrantID: "grant", URL: remote, Executable: executable, RegistryPath: filepath.Join(root, "user's config", "auth.json")})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(helper, "helper-only-secret") {
		t.Fatal("secret included in helper command")
	}
	cmd := exec.CommandContext(t.Context(), "git", "-c", key+".helper=", "-c", key+".helper="+helper, "credential", "fill")
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
	cmd.Stdin = strings.NewReader("url=" + remote + "\n\n")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "username=selected\npassword=helper-only-secret") {
		t.Fatalf("quoted helper invocation failed: %s, %v", out, err)
	}
}

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
