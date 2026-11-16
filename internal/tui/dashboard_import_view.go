package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func (m dashboardModel) importView() tea.View {
	run := m.importRun
	lines := []string{m.styles.brand.Render("Bearforge / Import repositories")}
	content := m.importWrappedContent()
	height := max(1, m.height-4)
	lines = append(lines, viewportLines(content, run.offset, m.width, height)...)
	help := "Esc back | PgUp/PgDn scroll | q quit | Ctrl-C cancel"
	switch run.phase {
	case importSource:
		help = "Tab field | Ctrl-U clear | Enter Load | ^W Bitbucket workspaces | ^L login | Esc back"
	case importLoginForm:
		help = "Tab field | Space provider | Ctrl-U clear | Enter login | Esc cancel"
	case importPicking:
		help = "Space select | / filter | e ID | Enter review | r reload | l login | c source | Esc close"
	case importReview:
		help = "y import | Esc selection | PgUp/PgDn scroll | Ctrl-C cancel"
	case importExecuting:
		help = "PgUp/PgDn scroll | q quit | Ctrl-C cancel and drain"
	case importResults:
		help = "v dashboard | PgUp/PgDn scroll | q quit | Ctrl-C cancel"
	}
	lines = append(lines, actionHelp(help, m.width, m.styles))
	return dashboardView(lines[:min(len(lines), m.height)], m.width)
}
func (m dashboardModel) importWrappedContent() []string {
	wrapped := ansi.Hardwrap(ansi.Wrap(strings.Join(m.importContent(), "\n"), m.width, ""), m.width, true)
	return strings.Split(wrapped, "\n")
}

func (m dashboardModel) importFollowField() {
	for row, line := range m.importWrappedContent() {
		if strings.HasPrefix(ansi.Strip(line), "> ") {
			m.importRun.offset = max(0, row-max(1, m.height-4)/2)
			return
		}
	}
}
func (m dashboardModel) importContent() []string {
	run := m.importRun
	lines := []string{}
	if run.err != nil && run.phase != importResults {
		label := "Error: "
		if run.phase == importPicking || run.phase == importWorkspaces {
			label = "API discovery: "
		}
		if run.phase == importResults {
			label = "Git/import: "
		}
		lines = append(lines, m.styles.bad.Render(label+displayText(run.err.Error())))
		if (run.phase == importPicking || run.phase == importWorkspaces) && run.source.Connection != "" {
			lines = append(lines, "Check API access: bearforge auth status --connection "+workspaceShellQuote(run.source.Connection), "Press l from repository selection to repair this connection with shared login.")
		}
	}
	if run.editingID {
		return append(lines, "Edit local ID (Ctrl-U clears): "+displayText(run.editID), "Enter saves; Escape cancels.")
	}
	if run.editing {
		return append(lines, "Filter: "+displayText(run.filter), "Enter applies; Escape clears.")
	}
	switch run.phase {
	case importConnections:
		rows := make([]string, 0, len(run.connections)+2)
		for i, c := range run.connections {
			prefix := "  "
			if i == run.index {
				prefix = "> "
			}
			rows = append(rows, prefix+displayText(c.Name))
		}
		for i, text := range []string{"New connection / guided token setup", "Environment token or anonymous public source"} {
			prefix := "  "
			if run.index == len(run.connections)+i {
				prefix = "> "
			}
			rows = append(rows, prefix+text)
		}
		lines = append(lines, listLines("Connections (local metadata)", rows, run.index, max(3, m.height-9), "No saved connections.")...)
		if run.index < len(run.connections) {
			c := run.connections[run.index]
			lines = append(lines, "", "Provider: "+displayText(c.Provider), "Host: "+displayText(c.Host), "Status: "+displayText(c.State), "Account: "+displayText(c.Account), "Login method: "+displayText(c.Method), "API: "+displayText(c.APIURL))
		}
		lines = append(lines, "Enter selects; r reloads connections.")
	case importSource:
		lines = append(lines, "Source settings / Enter Load starts provider HTTP.")
		if run.source.Connection != "" {
			lines = append(lines, "Connection: "+displayText(run.source.Connection), "Account: "+displayText(run.connection.Account), "Login method: "+displayText(run.connection.Method), "API status: "+displayText(run.connection.State), "Grant: "+displayText(run.source.GrantID))
		}
		labels := []string{"Scope (GitHub owner / Bitbucket workspace or project)", "Clone transport (Space toggles)", "Token environment variable name (optional)", "Provider (github / bitbucket / bitbucket-server)", "Host (hostname)", "API base URL", "Git base URL"}
		values := []string{run.source.Scope, run.transport, run.source.TokenEnv, run.source.Provider, run.source.Host, run.source.APIURL, run.source.GitURL}
		for i, label := range labels {
			prefix := "  "
			if i == run.field {
				prefix = "> "
			}
			lines = append(lines, prefix+label+": "+displayText(values[i]))
		}
		lines = append(lines, "API verification does not prove Git read access.", "SSH uses your existing keys/agent; HTTPS can use the selected grant.")
		if run.source.TokenEnv != "" {
			lines = append(lines, "API credential: environment variable "+displayText(run.source.TokenEnv), "Git uses existing helpers / SSH, independently of this API credential.")
		}
		if run.source.Provider == "github" && run.source.Connection == "" {
			lines = append(lines, "Anonymous GitHub browsing requires an owner or organization scope and lists public repositories.")
		}
		if run.source.Provider == "bitbucket" {
			lines = append(lines, "Enter a Bitbucket workspace scope, or Ctrl-W to enumerate accessible Bitbucket workspaces.")
		}
	case importLoginForm:
		lines = append(lines, "Guided token setup. No token belongs in these fields.")
		labels := []string{"Provider (Space toggles)", "Connection name", "Installation URL (including context path)", "API URL (optional override)", "Git URL (optional override)", "Account email / Git username (Bitbucket token)"}
		values := []string{run.login.Provider, run.login.Name, run.login.Host, run.login.APIURL, run.login.GitURL, run.login.Username}
		for i, label := range labels {
			prefix := "  "
			if i == run.field {
				prefix = "> "
			}
			lines = append(lines, prefix+label+": "+displayText(values[i]))
		}
		lines = append(lines, "Enter opens the provider's token setup page and prompts for a token with hidden input.")
		lines = append(lines, "Ctrl-E uses an environment token or anonymous public source instead.")
		for _, c := range run.connections {
			if c.Name == run.login.Name {
				lines = append(lines, "Logging in replaces this connection's grant. Existing repository associations must be reviewed again after replacement.")
				break
			}
		}
	case importLoggingIn:
		lines = append(lines, "Shared login is running; Escape cancels it.")
		if run.notice != "" {
			for _, line := range strings.Split(run.notice, "\n") {
				lines = append(lines, displayText(line))
			}
		}
		if run.prompt != nil {
			switch run.prompt.kind {
			case "secret":
				lines = append(lines, displayText(run.prompt.text)+": "+strings.Repeat("*", min(32, utf8RuneCount(run.secret))), "Enter submits hidden token.")
			case "confirm":
				c := run.prompt.connection
				lines = append(lines, "Confirm provider: "+displayText(c.Provider), "Installation: "+displayText(c.Host), "API: "+displayText(c.APIURL), "Git: "+displayText(c.GitURL), "Press y to authorize this provider and origin.")
			}
		}
	case importLoading:
		lines = append(lines, "Loading provider data... Escape cancels the request.")
	case importWorkspaces:
		rows := make([]string, 0, len(run.workspaces))
		for i, w := range run.workspaces {
			prefix := "  "
			if i == run.index {
				prefix = "> "
			}
			rows = append(rows, prefix+displayText(w.Slug))
		}
		lines = append(lines, listLines("Bitbucket workspaces", rows, run.index, max(3, m.height-8), "No workspaces loaded.")...)
		if run.index >= 0 && run.index < len(run.workspaces) {
			lines = append(lines, "", "Name: "+displayText(run.workspaces[run.index].Name), "Scope: "+displayText(run.workspaces[run.index].Slug))
		}
		lines = append(lines, "Enter selects the scope; Load remains explicit.")
		if len(run.workspaces) == 0 {
			lines = append(lines, "No Bitbucket workspaces loaded. Escape returns to direct scope; r retries.")
		}
	case importPicking:
		rows := m.importChoices()
		choices := make([]string, 0, len(rows))
		for i, c := range rows {
			prefix := "  [ ] "
			if run.selected[importKey(c)] {
				prefix = "  [x] "
			}
			if c.Disabled {
				prefix = "  [-] "
			}
			if i == run.index {
				prefix = ">" + prefix[1:]
			}
			choices = append(choices, prefix+displayText(c.Repository.FullName))
		}
		title := fmt.Sprintf("Repositories: %d / selected: %d", len(rows), m.importSelectionCount())
		lines = append(lines, listLines(title, choices, run.index, max(3, m.height-9), "No repositories match.")...)
		if run.filter != "" {
			lines = append(lines, "Filter: "+displayText(run.filter))
		}
		if len(rows) == 0 {
			lines = append(lines, "No repositories match. r retries discovery; c changes source.")
		}
		if run.index < len(rows) {
			c := rows[run.index]
			visibility := "public"
			if c.Repository.Private {
				visibility = "private"
			}
			if c.Repository.Archived {
				visibility += ", archived"
			}
			branch := c.Repository.DefaultBranch
			if branch == "" {
				branch = "(unknown)"
			}
			clone := c.Repository.HTTPSURL
			if run.transport == "ssh" {
				clone = c.Repository.SSHURL
			}
			lines = append(lines, "", "Local ID: "+displayText(run.ids[importKey(c)]), "Visibility: "+visibility, displayText(c.Repository.Description), "URL: "+displayText(clone), "Default branch: "+displayText(branch))
			if c.RegisteredID != "" {
				lines = append(lines, "Registered as: "+displayText(c.RegisteredID))
			}
			if c.RetiredID != "" {
				lines = append(lines, "Retired as: "+displayText(c.RetiredID)+"; use repo restore.")
			}
			if c.Problem != nil {
				lines = append(lines, displayText(c.Problem.Message))
			}
		}
	case importPlanning:
		lines = append(lines, "Previewing local IDs, clone destinations, and credential associations...", "This preview creates no clones or journals.")
	case importReview:
		lines = append(lines, "Review import destinations. Only y starts cloning.", "Repositories are registered without workspace attachment.")
		for _, r := range run.request.Repositories {
			clone := r.Repository.HTTPSURL
			if r.Transport == "ssh" {
				clone = r.Repository.SSHURL
			}
			lines = append(lines, "", m.styles.title.Render(displayText(r.ID)), "URL: "+displayText(clone))
			for _, item := range run.preview.Items {
				if item.Repository == r.ID {
					lines = append(lines, "Destination: "+displayText(item.Path))
					break
				}
			}
			if r.Source.Connection != "" {
				lines = append(lines, "Connection: "+displayText(r.Source.Connection)+" / grant "+displayText(r.Source.GrantID))
			}
			if r.Source.TokenEnv != "" {
				lines = append(lines, "API credential: environment variable "+displayText(r.Source.TokenEnv))
			}
			if r.Source.Connection != "" && r.Source.TokenEnv == "" && r.Transport == "https" {
				lines = append(lines, "Git integration: remote-scoped Bearforge credential helper for this URL.")
			} else {
				lines = append(lines, "Git integration: existing configured Git helpers / SSH agent.")
			}
		}
	case importExecuting, importResults:
		lines = append(lines, m.styles.title.Render(m.importSummary()))
		if run.phase == importExecuting {
			done := 0
			for _, s := range run.states {
				if s == "created" || s == "failed" || s == "unchanged" || s == "succeeded" {
					done++
				}
			}
			lines = append(lines, m.batchProgress(done, len(run.states), 0))
		}
		if run.result.Problem != nil && run.err == nil {
			lines = append(lines, displayText(run.result.Problem.Message))
		}
		names := make([]string, 0, len(run.request.Repositories))
		for _, repository := range run.request.Repositories {
			names = append(names, repository.ID)
		}
		lines = append(lines, m.repositoryStatusLines(names, run.states, m.width)...)
		if run.err != nil {
			lines = append(lines, "", m.styles.title.Render("Import failure"), displayText(run.err.Error()))
		}
		for _, r := range run.request.Repositories {
			for _, item := range run.result.Items {
				if item.Repository == r.ID && (item.RefFormat != "" || item.Problem != nil) {
					lines = append(lines, "", m.styles.title.Render(displayText(r.ID)))
					if item.RefFormat != "" {
						lines = append(lines, "Ref storage: "+displayText(item.RefFormat))
					}
					if item.Problem != nil {
						lines = append(lines, "Git/import: "+displayText(item.Problem.Message))
					}
				}
			}
		}
		if run.result.OperationID != "" {
			lines = append(lines, "", "Inspect: "+dashboardRecoveryCommand(run.root, "show", run.result.OperationID))
			if run.err != nil || run.result.Outcome == "partial" {
				lines = append(lines, "Resume: "+dashboardRecoveryCommand(run.root, "resume", run.result.OperationID))
			}
		}
		if run.err != nil {
			command := "bearforge auth status"
			if run.source.Connection != "" {
				command += " --connection " + workspaceShellQuote(run.source.Connection)
			}
			lines = append(lines, "Check API and Git separately: "+command, "Repair credentials with bearforge auth login, then resume the operation.")
		}
	}
	return lines
}

// Result summaries use the same Bubbles table as the CLI. When a name cannot
// fit beside its status, keep both complete as records in the outer viewport.
func (m dashboardModel) repositoryStatusLines(names []string, states map[string]string, width int) []string {
	if len(names) == 0 {
		return nil
	}
	nameWidth, stateWidth := len("Repository"), len("Status")
	rows := make([]table.Row, 0, len(names))
	for _, name := range names {
		name, state := displayText(name), displayText(states[name])
		nameWidth = max(nameWidth, ansi.StringWidth(name))
		stateWidth = max(stateWidth, ansi.StringWidth(state))
		rows = append(rows, table.Row{name, state})
	}
	if nameWidth+stateWidth+4 > width {
		lines := []string{}
		for _, row := range rows {
			lines = append(lines, "", m.styles.title.Render(row[0]), "Status: "+row[1])
		}
		return lines
	}
	component := table.New(table.WithColumns([]table.Column{{Title: "Repository", Width: nameWidth}, {Title: "Status", Width: stateWidth}}), table.WithRows(rows), table.WithWidth(nameWidth+stateWidth+4), table.WithHeight(len(rows)+1), table.WithFocused(false), table.WithStyles(table.Styles{Header: m.styles.title.PaddingRight(2), Cell: lipgloss.NewStyle().PaddingRight(2), Selected: lipgloss.NewStyle()}))
	return strings.Split(component.View(), "\n")
}
func utf8RuneCount(s string) int { return len([]rune(s)) }
func (m dashboardModel) importSelectionCount() int {
	n := 0
	for _, v := range m.importRun.selected {
		if v {
			n++
		}
	}
	return n
}
func (m dashboardModel) importSummary() string {
	run := m.importRun
	if run.phase == importExecuting {
		return "Importing repositories (API discovery and Git access are separate capabilities)"
	}
	ok, failed, notRun := 0, 0, 0
	for _, s := range run.states {
		switch s {
		case "created", "succeeded", "unchanged":
			ok++
		case "failed":
			failed++
		default:
			notRun++
		}
	}
	return fmt.Sprintf("Import: %d registered | %d failed | %d not run", ok, failed, notRun)
}
