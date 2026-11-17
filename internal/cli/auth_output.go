package cli

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/auth"
)

func (o *options) renderAuthentication(result app.Result) error {
	p := o.presentation(o.out, isTerminal(o.out))
	if _, err := fmt.Fprintln(p, authenticationDocument(result, p, outputWidth(o.out))); err != nil {
		return err
	}
	if result.Problem != nil {
		return o.diagnostic("Authentication:", result.Problem.Message)
	}
	return nil
}

func authenticationHeading(p *colorprofile.Writer, text string) string {
	if p.Profile > colorprofile.ASCII {
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")).Render(text)
	}
	return text
}

func authenticationIdentity(account string, verified, local bool) string {
	state := "unverified"
	if verified {
		state = "verified"
	} else if local {
		state = "not checked"
	}
	if account == "" {
		return state
	}
	return account + " (" + state + ")"
}

func authenticationState(state string) string {
	if state == "configured-unverified" {
		return "not checked"
	}
	return strings.ReplaceAll(state, "-", " ")
}

func authenticationDocument(result app.Result, p *colorprofile.Writer, width int) string {
	if width <= 0 {
		width = 80
	}
	title := "Authentication " + strings.TrimPrefix(result.Command, "auth.")
	parts := []string{authenticationHeading(p, repositoryListText(title))}
	if result.Outcome == "failed" {
		parts[0] += " [failed]"
	}
	switch data := result.Data.(type) {
	case auth.Connection:
		parts = append(parts, terminalRecords(p, [][]string{
			{"Connection", data.Name}, {"Provider", data.Provider}, {"Host", data.Host},
			{"Account", authenticationIdentity(data.Account, data.IdentityVerified, false)},
			{"API", authenticationState(data.State)}, {"API URL", data.APIURL}, {"Git URL", data.GitURL}, {"Login method", data.Method},
		}, width), "Git read: not checked; use bearforge auth status --repo NAME to verify access.")
	case authenticationStatusData:
		if len(data.Connections) == 0 && len(data.Repositories) == 0 {
			parts = append(parts, "No saved connections. Use bearforge auth login --provider github or bitbucket.")
			break
		}
		if data.Local {
			parts = append(parts, "Local configuration; credentials and remote access were not checked.")
		}
		connections := slices.Clone(data.Connections)
		slices.SortFunc(connections, func(a, b auth.StatusResult) int { return strings.Compare(a.Connection.Name, b.Connection.Name) })
		if len(connections) > 0 {
			rows := make([][]string, 0, len(connections))
			for _, status := range connections {
				c := status.Connection
				rows = append(rows, []string{c.Name, c.Provider, c.Host, authenticationIdentity(c.Account, status.IdentityVerified, data.Local), authenticationState(status.State)})
			}
			parts = append(parts, authenticationHeading(p, "Connections"), renderResultRows(p, []string{"CONNECTION", "PROVIDER", "HOST", "ACCOUNT", "API"}, rows, width))
		}
		repositories := slices.Clone(data.Repositories)
		slices.SortFunc(repositories, func(a, b repositoryAuthenticationStatus) int {
			return strings.Compare(a.Repository+"/"+a.Remote, b.Repository+"/"+b.Remote)
		})
		if len(repositories) > 0 {
			rows := make([][]string, 0, len(repositories))
			defaults := false
			details := []string{}
			for _, status := range repositories {
				source := status.Source
				if source == "" {
					source = status.Connection
				}
				if source == "" {
					source, defaults = "default", true
				}
				rows = append(rows, []string{status.Repository, status.Remote, source, authenticationState(status.API), authenticationState(status.GitRead)})
				if status.Detail != "" {
					details = append(details, repositoryListText(status.Repository+"/"+status.Remote+": "+status.Detail))
				}
			}
			parts = append(parts, authenticationHeading(p, "Repository access"), renderResultRows(p, []string{"REPOSITORY", "REMOTE", "API SOURCE", "API", "GIT READ"}, rows, width))
			if defaults {
				parts = append(parts, "Default: environment/provider defaults or a matching saved connection.")
			}
			if len(details) > 0 {
				parts = append(parts, authenticationHeading(p, "Details"), strings.Join(details, "\n"))
			}
		}
		parts = append(parts, "Git read is independent of API identity; push permission is not checked.")
	case auth.LogoutResult:
		parts = append(parts, terminalRecords(p, [][]string{{"Connection", data.Connection}, {"Status", "disconnected locally"}, {"Manage access", data.ManagementURL}}, width), "Provider token was not revoked.")
	default:
		if result.Outcome == "planned" {
			parts = append(parts, "Authentication action planned; no credentials changed.")
			if fields, ok := result.Data.(map[string]string); ok && fields["connection"] != "" {
				parts = append(parts, terminalRecords(p, [][]string{{"Connection", fields["connection"]}}, width))
			}
		}
	}
	return terminalDocument(strings.Join(parts, "\n\n"), width)
}
