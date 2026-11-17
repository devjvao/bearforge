package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/auth"
)

func authOutputFixture() app.Result {
	r := app.NewResult("auth.status")
	data := authenticationStatusData{Connections: []auth.StatusResult{{
		Connection: auth.Connection{Name: "work", Provider: "github", Host: "https://github.com", Account: "octocat", SecretRef: "opaque-secret-reference"},
		State:      auth.StateVerified, IdentityVerified: true,
	}}}
	for i := range 26 {
		connection := "work"
		if i == 0 {
			connection = ""
		}
		data.Repositories = append(data.Repositories, repositoryAuthenticationStatus{Repository: fmt.Sprintf("repo-%02d", i), Remote: "origin", Connection: connection, API: "readable", GitRead: "readable"})
	}
	r.Data = data
	return r
}

func TestAuthHumanStatusGroupsConnectionsAndRepositoryAccess(t *testing.T) {
	var output, diagnostics bytes.Buffer
	o := &options{out: &output, errOut: &diagnostics, color: "always"}
	if err := o.render(authOutputFixture()); err != nil {
		t.Fatal(err)
	}
	view := output.String()
	for _, want := range []string{"Authentication status", "Connections", "Repository access", "CONNECTION", "HOST", "ACCOUNT", "API SOURCE", "GIT READ", "octocat", "verified", "default"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q in organized auth status:\n%s", want, view)
		}
	}
	for i := range 26 {
		if !strings.Contains(view, fmt.Sprintf("repo-%02d", i)) {
			t.Errorf("repository row %d was hidden", i)
		}
	}
	if strings.Contains(view, "\x1b") || strings.Contains(view, "opaque-secret-reference") {
		t.Fatal("piped human output exposed styles or private connection metadata")
	}
	assertResultLinesFit(t, view, 80)
}

func TestAuthHumanStatusWrapsLongIdentitiesAndFailureDetails(t *testing.T) {
	r := app.NewResult("auth.status")
	r.Data = authenticationStatusData{Repositories: []repositoryAuthenticationStatus{{
		Repository: strings.Repeat("long-repository", 10), Remote: "origin", Connection: "work",
		API: "insufficient-permission", GitRead: "unavailable", Detail: "Request permission from the organization administrator before retrying.",
	}}}
	var output, diagnostics bytes.Buffer
	o := &options{out: &output, errOut: &diagnostics, color: "never"}
	if err := o.render(r); err != nil {
		t.Fatal(err)
	}
	assertResultLinesFit(t, output.String(), 80)
	plain := compactResultText(output.String())
	for _, want := range []string{strings.Repeat("long-repository", 10), "Requestpermissionfromtheorganizationadministratorbeforeretrying."} {
		if !strings.Contains(plain, want) {
			t.Errorf("auth status lost relevant detail %q", want)
		}
	}
}

func TestAuthMachineStatusRetainsItsDataContract(t *testing.T) {
	r := authOutputFixture()
	for _, mode := range []string{"json", "jsonl"} {
		var output bytes.Buffer
		o := &options{out: &output, output: mode}
		if err := o.render(r); err != nil {
			t.Fatal(err)
		}
		var got struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(output.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(got.Data, &fields); err != nil || len(fields) != 2 || fields["connections"] == nil || fields["repositories"] == nil {
			t.Fatalf("%s changed the auth status data fields: %s", mode, got.Data)
		}
		var rows []map[string]json.RawMessage
		if err := json.Unmarshal(fields["repositories"], &rows); err != nil || len(rows) != 26 {
			t.Fatalf("%s lost machine repository rows", mode)
		}
		for _, key := range []string{"repository", "remote", "api", "git_read"} {
			if rows[0][key] == nil {
				t.Errorf("%s lost machine field %s", mode, key)
			}
		}
		if rows[0]["Source"] != nil || rows[0]["source"] != nil || fields["Local"] != nil || fields["local"] != nil {
			t.Fatal("presentation metadata changed the machine schema")
		}
	}
}

func TestAuthDocumentsFitNarrowTerminalsAndSanitizeFields(t *testing.T) {
	for _, width := range []int{36, 60, 80, 160} {
		for _, profile := range []colorprofile.Profile{colorprofile.ASCII, colorprofile.TrueColor} {
			result := authOutputFixture()
			data := result.Data.(authenticationStatusData)
			data.Repositories[0].Repository = "api\x1b]0;injected-title\a\u202e"
			data.Repositories[0].Source = "env:WORK_TOKEN"
			result.Data = data
			view := authenticationDocument(result, &colorprofile.Writer{Profile: profile}, width)
			assertResultLinesFit(t, view, width)
			plain := ansi.Strip(view)
			if strings.Contains(view, "injected-title") || !strings.Contains(compactResultText(plain), "env:WORK_TOKEN") {
				t.Fatalf("unsafe or lost auth fields at width %d", width)
			}
			for _, r := range plain {
				if unicode.IsControl(r) && r != '\n' || r == '\u202e' {
					t.Fatalf("unsafe control character in auth output: %U", r)
				}
			}
		}
	}
}

func TestAuthActionsAndLocalStatusKeepUsefulFields(t *testing.T) {
	connection := auth.Connection{Name: "work", Provider: "github", Host: "https://github.example.com/installation", APIURL: "https://github.example.com/installation/api/v3", GitURL: "https://github.example.com/installation", Account: "octocat", IdentityVerified: true, State: "verified", Method: "token", SecretRef: "do-not-display-secret-reference"}
	for _, action := range []string{"login", "logout", "local", "planned"} {
		t.Run(action, func(t *testing.T) {
			result := app.NewResult("auth." + action)
			want := []string{"work"}
			switch action {
			case "login":
				result.Data = connection
				want = append(want, "octocat", "verified", connection.APIURL, connection.GitURL, "not checked")
			case "logout":
				result.Data = auth.LogoutResult{Connection: "work", ManagementURL: "https://github.example.com/settings/tokens", LocalOnly: true}
				want = append(want, "disconnected locally", "Provider token was not revoked", "https://github.example.com/settings/tokens")
			case "local":
				result.Command = "auth.status"
				result.Data = authenticationStatusData{Local: true, Connections: []auth.StatusResult{{Connection: connection, State: auth.StateUnverified}}}
				want = append(want, "Local configuration", "octocat (not checked)")
			case "planned":
				result.Command, result.Outcome = "auth.logout", "planned"
				result.Data = map[string]string{"connection": "work"}
				want = append(want, "no credentials changed")
			}
			for _, width := range []int{36, 80, 160} {
				view := authenticationDocument(result, &colorprofile.Writer{Profile: colorprofile.ASCII}, width)
				assertResultLinesFit(t, view, width)
				for _, value := range want {
					if !strings.Contains(compactResultText(view), compactResultText(value)) {
						t.Errorf("width %d lost %q in %s", width, value, view)
					}
				}
				if strings.Contains(view, "do-not-display") || strings.Contains(view, "opaque-secret") {
					t.Fatal("auth action exposed internal secret references")
				}
			}
		})
	}
}

func TestAuthFailureDiagnosticsSanitizeRedirectedOutput(t *testing.T) {
	var output, diagnostics bytes.Buffer
	o := &options{out: &output, errOut: &diagnostics, color: "always"}
	result := app.NewResult("auth.status")
	result.Outcome = "failed"
	result.Problem = &app.Problem{Code: "unavailable", Message: "work\x1b]0;injected-title\a\x1b[2J\u202e: credential unavailable"}
	if err := o.render(result); err != nil {
		t.Fatal(err)
	}
	view := diagnostics.String()
	if strings.ContainsAny(view, "\x1b\u202e\a") || strings.Contains(view, "injected-title") {
		t.Fatalf("unsafe redirected diagnostic: %q", view)
	}
	if !strings.Contains(view, "work") || !strings.Contains(view, "credential unavailable") {
		t.Fatal("sanitization removed the useful diagnostic")
	}
}

func TestAuthFailureDiagnosticsKeepJoinedErrorsSeparated(t *testing.T) {
	var output, diagnostics bytes.Buffer
	o := &options{out: &output, errOut: &diagnostics, color: "always"}
	result := app.NewResult("auth.status")
	result.Outcome = "failed"
	result.Problem = app.ProblemFor(errors.Join(
		errors.New("connection github: rejected"),
		errors.New("repository auriset API access: rejected"),
		errors.New("multiple saved connections match; select a connection"),
	))
	if err := o.render(result); err != nil {
		t.Fatal(err)
	}
	view := diagnostics.String()
	for _, failure := range []string{"connection github: rejected", "repository auriset API access: rejected", "multiple saved connections match; select a connection"} {
		matchingLines := 0
		for _, line := range strings.Split(view, "\n") {
			if strings.Contains(line, failure) {
				matchingLines++
				if strings.Count(line, "rejected")+strings.Count(line, "multiple saved connections") != 1 {
					t.Fatalf("joined errors share one unreadable line: %q", line)
				}
			}
		}
		if matchingLines != 1 {
			t.Fatalf("expected one visible diagnostic line for %q: %q", failure, view)
		}
	}
	if strings.Contains(view, "\x1b") {
		t.Fatal("redirected diagnostics contain terminal styling")
	}
}
