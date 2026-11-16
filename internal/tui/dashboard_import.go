package tui

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"bearforge/internal/app"
	"bearforge/internal/auth"
	"bearforge/internal/domain"
)

// DashboardImporter is an explicit-action capability. Observers finish before
// ImportRepositories returns; login hooks must honor their supplied context.
type DashboardImporter interface {
	ImportConnections(context.Context) ([]auth.Connection, error)
	ImportLogin(context.Context, auth.LoginRequest, auth.LoginHooks) (auth.Connection, error)
	DiscoverRepositories(context.Context, domain.RepositorySource) ([]domain.RemoteRepository, error)
	DiscoverWorkspaces(context.Context, domain.RepositorySource) ([]domain.ProviderWorkspace, error)
	RepositoryImportChoices(context.Context, domain.RepositorySource, []domain.RemoteRepository) ([]app.RepositoryImportChoice, error)
	ImportRepositories(context.Context, app.ImportRequest, func(app.Event)) (app.Result, error)
}

type importPhase uint8

const (
	importConnections importPhase = iota
	importSource
	importLoginForm
	importLoggingIn
	importLoading
	importWorkspaces
	importPicking
	importPlanning
	importReview
	importExecuting
	importResults
)

type importReply uint8

const (
	importConnectionsLoaded importReply = iota
	importLoggedIn
	importDiscovered
	importWorkspacesLoaded
	importPreviewed
	importFinished
)

type dashboardImportMsg struct {
	id          uint64
	kind        importReply
	connections []auth.Connection
	connection  auth.Connection
	choices     []app.RepositoryImportChoice
	workspaces  []domain.ProviderWorkspace
	result      app.Result
	err         error
}
type dashboardImportProgressMsg struct {
	id     uint64
	states map[string]string
}
type dashboardImportSignal struct {
	kind, text string
	connection auth.Connection
	response   chan string
}
type dashboardImportSignalMsg struct {
	id     uint64
	signal dashboardImportSignal
}
type dashboardImport struct {
	ctx                  context.Context
	cancel               context.CancelFunc
	phase                importPhase
	root                 string
	connections          []auth.Connection
	connection           auth.Connection
	source               domain.RepositorySource
	transport            string
	login                auth.LoginRequest
	choices              []app.RepositoryImportChoice
	workspaces           []domain.ProviderWorkspace
	selected             map[string]bool
	ids                  map[string]string
	index, field, offset int
	filter, editID       string
	editing, editingID   bool
	request              app.ImportRequest
	preview, result      app.Result
	err                  error
	states               map[string]string
	progress             *dashboardProgress
	signals              chan dashboardImportSignal
	loginDone            chan struct{}
	prompt               *dashboardImportSignal
	secret, notice       string
}

func (m dashboardModel) openImport() (tea.Model, tea.Cmd) {
	if _, ok := m.reader.source.(DashboardImporter); !ok || m.loading || m.loadErr != nil || len(m.data.PendingOperations) > 0 || (m.syncRun != nil && m.syncRun.running) || m.workspaceActionBusy() || (m.addRun != nil && m.addRun.phase != addResults) {
		return m, nil
	}
	m.cancel()
	m.requestID++
	ctx, cancel := context.WithCancel(m.reader.ctx)
	m.importRun = &dashboardImport{ctx: ctx, cancel: cancel, root: m.data.Root, phase: importConnections, transport: "https", selected: map[string]bool{}, ids: map[string]string{}}
	m.showImport = true
	return m.importCommand(importConnectionsLoaded)
}

// All operation closures capture immutable request data and use the reader gate.
func (m dashboardModel) importCommand(kind importReply) (tea.Model, tea.Cmd) {
	m.importID++
	run := m.importRun
	run.err = nil
	run.offset = 0
	id, ctx, reader := m.importID, run.ctx, m.reader
	source, req := run.source, run.request
	req.Repositories = slices.Clone(req.Repositories)
	capability := reader.source.(DashboardImporter)
	return m, func() tea.Msg {
		out := dashboardImportMsg{id: id, kind: kind}
		release, err := reader.acquire(ctx)
		if err != nil {
			out.err = err
			return out
		}
		defer release()
		switch kind {
		case importConnectionsLoaded:
			out.connections, out.err = capability.ImportConnections(ctx)
		case importDiscovered:
			var repos []domain.RemoteRepository
			repos, out.err = capability.DiscoverRepositories(ctx, source)
			if out.err == nil {
				out.choices, out.err = capability.RepositoryImportChoices(ctx, source, repos)
			}
		case importWorkspacesLoaded:
			out.workspaces, out.err = capability.DiscoverWorkspaces(ctx, source)
		case importPreviewed:
			out.result, out.err = capability.ImportRepositories(ctx, req, nil)
		}
		return out
	}
}
func importSourceFor(c auth.Connection) domain.RepositorySource {
	provider := c.Provider
	if provider == "bitbucket-cloud" {
		provider = "bitbucket"
	}
	host := c.Host
	if u, err := url.Parse(c.Host); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	return domain.RepositorySource{Provider: provider, Host: host, APIURL: c.APIURL, GitURL: c.GitURL, Connection: c.Name, GrantID: c.GrantID}
}
func importKey(c app.RepositoryImportChoice) string {
	r := c.Repository
	identity := r.ID
	if identity == "" {
		identity = r.FullName
	}
	return strings.Join([]string{r.Provider, r.Host, r.APIURL, identity}, "\x00")
}
func (m dashboardModel) importChoices() []app.RepositoryImportChoice {
	rows := []app.RepositoryImportChoice{}
	for _, c := range m.importRun.choices {
		if strings.Contains(strings.ToLower(c.Repository.FullName+" "+c.Repository.Description+" "+m.importRun.ids[importKey(c)]), strings.ToLower(m.importRun.filter)) {
			rows = append(rows, c)
		}
	}
	return rows
}
func (m dashboardModel) previewImport() (tea.Model, tea.Cmd) {
	run := m.importRun
	req := app.ImportRequest{DryRun: true}
	ids := map[string]bool{}
	for _, c := range run.choices {
		key := importKey(c)
		if c.Disabled || !run.selected[key] {
			continue
		}
		id := run.ids[key]
		folded := strings.ToLower(id)
		if id == "" || ids[folded] {
			run.err = errors.New("selected local IDs must be nonempty and unique, ignoring case; press e to edit")
			return m, nil
		}
		ids[folded] = true
		req.Repositories = append(req.Repositories, app.ImportRepository{ID: id, Repository: c.Repository, Source: c.Source, Transport: run.transport})
	}
	if len(req.Repositories) == 0 {
		run.err = errors.New("select at least one repository with Space")
		return m, nil
	}
	run.request = req
	run.preview = app.Result{}
	run.phase = importPlanning
	return m.importCommand(importPreviewed)
}
func validateImportPreview(req app.ImportRequest, result app.Result) error {
	if result.Problem != nil || result.PlanDigest == "" || len(result.Items) != len(req.Repositories) {
		return errors.New("the import preview is incomplete; retry before confirming")
	}
	seen := map[string]bool{}
	for _, item := range result.Items {
		found := false
		for _, repo := range req.Repositories {
			if repo.ID == item.Repository {
				found = true
				break
			}
		}
		if !found || seen[item.Repository] || item.Path == "" || item.Problem != nil || item.Outcome != "planned" {
			return errors.New("the import preview does not match the selection; retry before confirming")
		}
		seen[item.Repository] = true
	}
	return nil
}
func (m dashboardModel) confirmImport() (tea.Model, tea.Cmd) {
	run := m.importRun
	if run.phase != importReview || run.err != nil || run.preview.PlanDigest == "" || m.width < 36 || m.height < 12 {
		return m, nil
	}
	req := run.request
	req.Repositories = slices.Clone(req.Repositories)
	req.DryRun = false
	req.ExpectedPlan = run.preview.PlanDigest
	run.phase = importExecuting
	run.offset = 0
	run.states = map[string]string{}
	names := []string{}
	for _, r := range req.Repositories {
		names = append(names, r.ID)
		run.states[r.ID] = "queued"
	}
	run.progress = newDashboardProgress(names)
	m.syncRun, m.showSync = nil, false
	m.addRun, m.showAdd = nil, false
	m.workspaceRun, m.showWorkspace = nil, false
	m.importID++
	id, reader, ctx, progress := m.importID, m.reader, run.ctx, run.progress
	capability := reader.source.(DashboardImporter)
	work := func() tea.Msg {
		defer close(progress.done)
		release, err := reader.acquire(ctx)
		if err != nil {
			return dashboardImportMsg{id: id, kind: importFinished, err: err}
		}
		defer release()
		result, err := capability.ImportRepositories(ctx, req, progress.observe)
		return dashboardImportMsg{id: id, kind: importFinished, result: result, err: err}
	}
	return m, tea.Batch(work, m.importProgressCommand())
}
func (m dashboardModel) importProgressCommand() tea.Cmd {
	id, run := m.importID, m.importRun
	return func() tea.Msg {
		if states, ok := run.progress.next(run.ctx); ok {
			return dashboardImportProgressMsg{id: id, states: states}
		}
		return nil
	}
}
func (m dashboardModel) updateImport(msg tea.Msg) (tea.Model, tea.Cmd) {
	run := m.importRun
	if run == nil {
		return m, nil
	}
	switch v := msg.(type) {
	case dashboardImportSignalMsg:
		if v.id != m.importID || run.phase != importLoggingIn {
			return m, nil
		}
		run.offset = 0
		if v.signal.kind == "notice" {
			run.notice = v.signal.text
		} else {
			signal := v.signal
			run.prompt = &signal
			run.secret = ""
		}
		return m, m.importSignalCommand()
	case dashboardImportProgressMsg:
		if v.id != m.importID || run.phase != importExecuting {
			return m, nil
		}
		for id, state := range v.states {
			if _, ok := run.states[id]; ok {
				run.states[id] = state
			}
		}
		return m, m.importProgressCommand()
	case dashboardImportMsg:
		if v.id != m.importID {
			return m, nil
		}
		run.err = v.err
		run.offset = 0
		switch v.kind {
		case importConnectionsLoaded:
			if run.phase != importConnections {
				return m, nil
			}
			run.connections = v.connections
			if len(v.connections) == 0 && v.err == nil {
				run.phase = importLoginForm
				run.login = auth.LoginRequest{Provider: "github", Host: "https://github.com", Name: "github"}
			}
		case importLoggedIn:
			if run.phase != importLoggingIn {
				return m, nil
			}
			run.prompt = nil
			run.secret = ""
			if v.err != nil {
				run.phase = importLoginForm
				return m, nil
			}
			run.connection = v.connection
			run.source = importSourceFor(v.connection)
			run.phase = importSource
			run.field = 0
		case importDiscovered:
			if run.phase != importLoading {
				return m, nil
			}
			run.phase = importPicking
			if v.err == nil {
				run.choices = v.choices
				run.index = 0
				for _, c := range v.choices {
					key := importKey(c)
					if _, ok := run.ids[key]; !ok {
						run.ids[key] = c.ID
					}
					if c.Disabled {
						delete(run.selected, key)
					}
				}
			}
		case importWorkspacesLoaded:
			if run.phase != importLoading {
				return m, nil
			}
			run.phase = importWorkspaces
			run.workspaces = v.workspaces
			run.index = 0
		case importPreviewed:
			if run.phase != importPlanning {
				return m, nil
			}
			run.phase = importReview
			run.preview = v.result
			if run.err == nil {
				run.err = validateImportPreview(run.request, v.result)
			}
		case importFinished:
			if run.phase != importExecuting {
				return m, nil
			}
			run.phase = importResults
			run.result = v.result
			run.cancel()
			for id := range run.states {
				run.states[id] = "not run"
			}
			for _, item := range v.result.Items {
				if _, ok := run.states[item.Repository]; ok && item.Outcome != "planned" {
					run.states[item.Repository] = item.Outcome
				}
			}
			return m.request(m.workspace)
		}
	}
	return m, nil
}
func (m dashboardModel) closeImport() (tea.Model, tea.Cmd) {
	if m.importRun.phase != importResults {
		m.importRun.cancel()
		m.importRun.secret = ""
		m.importRun = nil
		m.importID++
	}
	m.showImport = false
	return m, nil
}
func importEdit(value *string, key tea.KeyPressMsg, limit int) {
	switch key.String() {
	case "ctrl+u":
		*value = ""
	case "backspace":
		_, size := utf8.DecodeLastRuneInString(*value)
		*value = (*value)[:len(*value)-size]
	default:
		if len(*value)+len(key.Text) <= limit {
			*value += displayText(key.Text)
		}
	}
}
func (m dashboardModel) importText(text string) dashboardModel {
	run := m.importRun
	if run == nil {
		return m
	}
	// Paste is treated as literal input only in a dedicated editable field.
	if run.phase == importLoggingIn && run.prompt != nil && run.prompt.kind == "secret" {
		if len(run.secret)+len(text) <= 16384 && !strings.ContainsAny(text, "\r\n\x00") {
			run.secret += text
		}
	} else if target := m.importFormTarget(); target != nil {
		if len(*target)+len(text) <= 2048 {
			*target += displayText(text)
		}
	}
	return m
}
func (m dashboardModel) importFormTarget() *string {
	run := m.importRun
	if run.editingID {
		return &run.editID
	}
	if run.editing {
		return &run.filter
	}
	if run.phase == importSource {
		fields := []*string{&run.source.Scope, &run.transport, &run.source.TokenEnv, &run.source.Provider, &run.source.Host, &run.source.APIURL, &run.source.GitURL}
		if run.field == 1 {
			return nil
		}
		return fields[run.field]
	}
	if run.phase == importLoginForm {
		fields := []*string{&run.login.Provider, &run.login.Name, &run.login.Host, &run.login.APIURL, &run.login.GitURL, &run.login.Username}
		if run.field == 0 {
			return nil
		}
		return fields[run.field]
	}
	return nil
}
func (m dashboardModel) importKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	run := m.importRun
	name := key.String()
	if name == "esc" && run.phase != importExecuting {
		if run.editingID {
			run.editingID = false
			return m, nil
		}
		if run.editing {
			run.editing = false
			run.filter = ""
			run.index = 0
			return m, nil
		}
		if run.phase == importReview {
			run.phase = importPicking
			run.err = nil
			run.preview = app.Result{}
			return m, nil
		}
		if run.phase == importWorkspaces {
			run.phase = importSource
			run.err = nil
			return m, nil
		}
		return m.closeImport()
	}
	if run.editing || run.editingID {
		if name == "enter" {
			if run.editingID {
				rows := m.importChoices()
				if run.index < len(rows) {
					run.ids[importKey(rows[run.index])] = run.editID
				}
				run.editingID = false
			} else {
				run.editing = false
			}
			return m, nil
		}
		if run.editingID {
			importEdit(&run.editID, key, 128)
		} else {
			importEdit(&run.filter, key, 256)
			run.index = 0
		}
		return m, nil
	}
	if run.phase == importLoggingIn && run.prompt != nil && run.prompt.kind == "secret" {
		if name == "enter" {
			value := run.secret
			run.secret = ""
			run.prompt.response <- value
			run.prompt = nil
		} else {
			importEdit(&run.secret, key, 16384)
		}
		return m, nil
	}
	if name == "q" && run.phase != importSource && run.phase != importLoginForm {
		return m.stop(nil)
	}
	if m.width < 36 || m.height < 12 {
		return m, nil
	}
	if name == "pgdown" || name == "pgup" {
		delta := max(1, m.height-6)
		if name == "pgup" {
			delta = -delta
		}
		run.offset = max(0, min(max(0, len(m.importWrappedContent())-1), run.offset+delta))
		return m, nil
	}
	switch run.phase {
	case importConnections:
		switch name {
		case "down", "j":
			run.index = min(len(run.connections)+1, run.index+1)
		case "up", "k":
			run.index = max(0, run.index-1)
		case "r":
			return m.importCommand(importConnectionsLoaded)
		case "enter":
			if run.index < len(run.connections) {
				run.connection = run.connections[run.index]
				run.source = importSourceFor(run.connection)
				run.phase = importSource
			} else if run.index == len(run.connections) {
				run.phase = importLoginForm
				run.login = auth.LoginRequest{Provider: "github", Host: "https://github.com", Name: "github"}
			} else {
				m.importEnvironmentSource()
			}
			run.field = 0
			run.err = nil
		}
	case importSource, importLoginForm:
		count := 7
		if run.phase == importLoginForm {
			count = 6
		}
		switch name {
		case "ctrl+l":
			if run.phase == importSource {
				m.importRelogin()
			}
		case "ctrl+e":
			if run.phase == importLoginForm {
				m.importEnvironmentSource()
			}
		case "tab":
			run.field = (run.field + 1) % count
		case "shift+tab":
			run.field = (run.field + count - 1) % count
		case "enter":
			if run.phase == importLoginForm {
				return m.startImportLogin()
			}
			if run.transport != "https" && run.transport != "ssh" {
				run.err = errors.New("transport must be https or ssh")
				return m, nil
			}
			run.phase = importLoading
			return m.importCommand(importDiscovered)
		case "ctrl+w":
			if run.phase == importSource && run.source.Provider == "bitbucket" {
				run.phase = importLoading
				return m.importCommand(importWorkspacesLoaded)
			}
		case "space", " ", "left", "right":
			if run.phase == importSource && run.field == 1 {
				if run.transport == "https" {
					run.transport = "ssh"
				} else {
					run.transport = "https"
				}
				return m, nil
			}
			if run.phase == importLoginForm && run.field == 0 {
				if run.login.Provider == "github" {
					run.login.Provider = "bitbucket"
					run.login.Host = "https://bitbucket.org"
					run.login.Name = "bitbucket"
				} else {
					run.login.Provider = "github"
					run.login.Host = "https://github.com"
					run.login.Name = "github"
				}
				return m, nil
			}
			if target := m.importFormTarget(); target != nil {
				importEdit(target, key, 2048)
			}
		default:
			if target := m.importFormTarget(); target != nil {
				importEdit(target, key, 2048)
			}
		}
	case importLoggingIn:
		if run.prompt == nil {
			return m, nil
		}
		switch run.prompt.kind {
		case "confirm":
			if name == "y" {
				run.prompt.response <- "yes"
				run.prompt = nil
			}
		}
	case importWorkspaces:
		switch name {
		case "down", "j":
			run.index = min(len(run.workspaces)-1, run.index+1)
		case "up", "k":
			run.index = max(0, run.index-1)
		case "enter":
			if run.index >= 0 && run.index < len(run.workspaces) {
				run.source.Scope = run.workspaces[run.index].Slug
				run.phase = importSource
			}
		case "r":
			run.phase = importLoading
			return m.importCommand(importWorkspacesLoaded)
		}
	case importPicking:
		rows := m.importChoices()
		switch name {
		case "down", "j":
			run.index = max(0, min(len(rows)-1, run.index+1))
		case "up", "k":
			run.index = max(0, run.index-1)
		case "space", " ":
			if run.index < len(rows) && !rows[run.index].Disabled {
				key := importKey(rows[run.index])
				run.selected[key] = !run.selected[key]
				run.err = nil
			}
		case "e":
			if run.index < len(rows) && !rows[run.index].Disabled {
				run.editID = run.ids[importKey(rows[run.index])]
				run.editingID = true
			}
		case "/":
			run.editing = true
		case "enter":
			return m.previewImport()
		case "r":
			run.phase = importLoading
			return m.importCommand(importDiscovered)
		case "c":
			run.phase = importSource
			run.field = 0
			run.err = nil
		case "l":
			m.importRelogin()
		}
	case importReview:
		switch name {
		case "y", "Y":
			return m.confirmImport()
		case "r":
			return m.previewImport()
		case "n":
			run.phase = importPicking
			run.err = nil
		}
	case importResults:
		if name == "v" {
			return m.closeImport()
		}
	}
	if run.phase == importSource || run.phase == importLoginForm {
		m.importFollowField()
	}
	return m, nil
}

func (m dashboardModel) importRelogin() {
	run := m.importRun
	c := run.connection
	provider := c.Provider
	if provider == "" {
		provider = run.source.Provider
	}
	host := c.Host
	if host == "" && run.source.GitURL != "" {
		host = run.source.GitURL
	}
	run.login = auth.LoginRequest{Name: c.Name, Provider: provider, Host: host, APIURL: c.APIURL, GitURL: c.GitURL, Username: c.Username}
	run.phase, run.field, run.offset, run.err = importLoginForm, 0, 0, nil
}

func (m dashboardModel) importEnvironmentSource() {
	run := m.importRun
	run.phase, run.field, run.err = importSource, 0, nil
	run.connection = auth.Connection{}
	run.source = domain.RepositorySource{Provider: "github", Host: "github.com", APIURL: "https://api.github.com", GitURL: "https://github.com"}
}
