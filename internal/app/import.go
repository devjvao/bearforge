package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"unicode"

	"bearforge/internal/domain"
)

// ImportRepository is an explicit discovery selection and its editable local ID.
type ImportRepository struct {
	ID         string
	Repository domain.RemoteRepository
	Source     domain.RepositorySource
	Transport  string
}

type ImportRequest struct {
	Repositories []ImportRepository
	DryRun       bool
	ExpectedPlan string
	RefFormat    string
}

// ImportPlan is the nonsecret review payload. Actual ref storage is selected
// during journal-owned execution and reported in each resulting Item.
type ImportPlan struct {
	Root            string
	CatalogRevision string
	Repositories    []ImportRepository
	Steps           []Step
}

type RepositoryImportChoice struct {
	ID           string
	Repository   domain.RemoteRepository
	Source       domain.RepositorySource
	RegisteredID string
	RetiredID    string
	Disabled     bool
	Problem      *Problem
}

// RepositoryImportChoices annotates observations without discovery or Git calls.
// Conflicting proposed IDs remain editable; registered/retired identities cannot
// be selected for a second clone.
func (s *Service) RepositoryImportChoices(ctx context.Context, source domain.RepositorySource, repositories []domain.RemoteRepository) ([]RepositoryImportChoice, error) {
	c, _, err := s.Store.LoadCatalog()
	if err != nil {
		return nil, err
	}
	choices := make([]RepositoryImportChoice, 0, len(repositories))
	counts := map[string]int{}
	for _, remote := range repositories {
		counts[proposedImportID(remote)]++
	}
	for _, remote := range repositories {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		choice := RepositoryImportChoice{ID: proposedImportID(remote), Repository: remote, Source: source}
		selected := ImportRepository{ID: choice.ID, Repository: remote, Source: source}
		_, repo, err := normalizeImport(selected)
		if err == nil {
			identity := repo
			identity.ID = ""
			choice.RegisteredID, choice.RetiredID = importConflict(c, identity)
			if choice.RegisteredID != "" {
				choice.Disabled = true
				err = domain.E("conflict", "repository is already registered as %s", choice.RegisteredID)
			}
			if choice.RetiredID != "" {
				choice.Disabled = true
				err = domain.E("conflict", "repository is retired as %s; use repo restore", choice.RetiredID)
			}
			if err == nil {
				if _, ok := c.Repositories[repo.ID]; ok {
					err = domain.E("conflict", "local ID %s is already registered; edit this ID", repo.ID)
				}
			}
			if err == nil {
				if _, ok := c.Retired[repo.ID]; ok {
					err = domain.E("conflict", "local ID %s is retired; edit this ID", repo.ID)
				}
			}
			if err == nil && counts[choice.ID] > 1 {
				err = domain.E("conflict", "proposed local ID %s is shared; edit the selected IDs", choice.ID)
			}
			if err == nil {
				err = s.importDestination(repo.ID)
			}
		}
		choice.Problem = ProblemFor(err)
		choices = append(choices, choice)
	}
	return choices, nil
}

func proposedImportID(r domain.RemoteRepository) string {
	name := r.Name
	if name == "" {
		parts := strings.Split(r.FullName, "/")
		name = parts[len(parts)-1]
	}
	var b strings.Builder
	for _, ch := range strings.ToLower(name) {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || strings.ContainsRune("._-", ch) {
			b.WriteRune(ch)
		} else {
			b.WriteByte('-')
		}
	}
	id := strings.Trim(b.String(), ".-_")
	if len(id) > 64 {
		id = strings.TrimRight(id[:64], ".")
	}
	if domain.ValidateID(id) != nil {
		id = "repo-" + id
		if len(id) > 64 {
			id = id[:64]
		}
	}
	return id
}

func (s *Service) ImportRepositories(ctx context.Context, req ImportRequest) (Result, error) {
	r := NewResult("repo.import")
	if err := ValidateRefFormat(req.RefFormat); err != nil {
		return r, err
	}
	if len(req.Repositories) == 0 {
		return r, domain.E("invalid", "select at least one repository")
	}
	if !req.DryRun {
		release, err := s.lock(ctx, "")
		if err != nil {
			return r, err
		}
		defer release()
	}
	c, revision, err := s.Store.LoadCatalog()
	if err != nil {
		return r, err
	}
	selection := append([]ImportRepository(nil), req.Repositories...)
	steps := make([]Step, 0, len(selection))
	seen := map[string]bool{}
	for i, selected := range selection {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		if selected.ID == "" {
			selected.ID = proposedImportID(selected.Repository)
		}
		selected, repo, err := normalizeImport(selected)
		if err != nil {
			return r, err
		}
		selection[i] = selected
		if seen[repo.ID] {
			return r, domain.E("conflict", "duplicate local ID %s", repo.ID)
		}
		seen[repo.ID] = true
		active, retired := importConflict(c, repo)
		if active != "" {
			return r, domain.E("conflict", "repository is already registered as %s", active)
		}
		if retired != "" {
			return r, domain.E("conflict", "repository is retired as %s; use repo restore", retired)
		}
		for _, previous := range steps {
			if repositoriesEquivalent(*previous.ImportedRepository, repo) {
				return r, domain.E("conflict", "selection contains the same remote repository twice")
			}
		}
		if err := s.importDestination(repo.ID); err != nil {
			return r, err
		}
		if err := s.validateImportCredentials(ctx, repo, false); err != nil {
			return r, err
		}
		steps = append(steps, Step{Repository: repo.ID, ImportedRepository: &repo, State: "planned", RequestedRefFormat: req.RefFormat})
	}
	sort.Slice(steps, func(i, j int) bool { return steps[i].Repository < steps[j].Repository })
	sort.Slice(selection, func(i, j int) bool { return selection[i].ID < selection[j].ID })
	r.PlanDigest = importPlanDigest(s.Store.RootPath(), revision, selection, steps)
	r.Data = ImportPlan{Root: s.Store.RootPath(), CatalogRevision: revision, Repositories: selection, Steps: steps}
	for _, step := range steps {
		path, _ := s.repoPath(step.Repository)
		r.Items = append(r.Items, Item{Repository: step.Repository, Path: path, Outcome: "planned"})
	}
	if req.DryRun {
		r.Outcome = "planned"
		return r, nil
	}
	if req.ExpectedPlan == "" || req.ExpectedPlan != r.PlanDigest {
		return r, domain.E("conflict", "import preview changed; review a new preview before importing")
	}
	op := newOperation(s.Store.RootPath(), "repo.import", "")
	op.Steps = steps
	if err := s.Journal.Save(op); err != nil {
		return r, err
	}
	return s.applyImport(ctx, op)
}

func importPlanDigest(root, revision string, selection []ImportRepository, steps []Step) string {
	b, _ := json.Marshal(struct {
		Root, Revision string
		Selection      []ImportRepository
		Steps          []Step
	}{root, revision, selection, steps})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (s *Service) importDestination(id string) error {
	p, err := s.repoPath(id)
	if err != nil {
		return err
	}
	exists, err := s.Store.Exists("repos", id+".git")
	if err != nil {
		return err
	}
	if exists {
		return domain.E("conflict", "unregistered destination already exists: %s", p)
	}
	if reader, ok := s.Store.(directoryReader); ok {
		entries, err := reader.ReadDirectory("repos")
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if strings.EqualFold(entry.Name, id+".git") {
				return domain.E("conflict", "case-equivalent import destination already exists: %s", entry.Name)
			}
		}
	}
	return nil
}

func normalizeImport(selected ImportRepository) (ImportRepository, domain.Repository, error) {
	empty := domain.Repository{}
	if err := domain.ValidateID(selected.ID); err != nil {
		return selected, empty, err
	}
	r := selected.Repository
	source := selected.Source
	if selected.Transport == "" {
		selected.Transport = "https"
	}
	if source.Provider == "" {
		source.Provider = r.Provider
	}
	if source.Host == "" {
		source.Host = r.Host
	}
	if source.APIURL == "" {
		source.APIURL = r.APIURL
	}
	source.Host = strings.ToLower(source.Host)
	source.APIURL = strings.TrimRight(source.APIURL, "/")
	if r.Provider != source.Provider || !strings.EqualFold(r.Host, source.Host) || strings.TrimRight(r.APIURL, "/") != source.APIURL {
		return selected, empty, domain.E("invalid", "discovered repository does not match its selected provider source")
	}
	remote := r.HTTPSURL
	if selected.Transport == "ssh" {
		remote = r.SSHURL
	} else if selected.Transport != "https" {
		return selected, empty, domain.E("invalid", "clone transport must be https or ssh")
	}
	if err := validateImportURL(remote, selected.Transport); err != nil {
		return selected, empty, err
	}
	if r.ID == "" || r.FullName == "" || r.Name == "" {
		return selected, empty, domain.E("invalid", "discovered repository identity is incomplete")
	}
	for _, u := range []struct{ raw, transport string }{{r.HTTPSURL, "https"}, {r.SSHURL, "ssh"}} {
		if u.raw != "" {
			if err := validateImportURL(u.raw, u.transport); err != nil {
				return selected, empty, err
			}
		}
	}
	provider := map[string]string{"kind": source.Provider, "host": source.Host, "api_url": source.APIURL, "repository": r.FullName, "source_repository": r.FullName, "remote_id": r.ID, "connection": source.Connection, "grant_id": source.GrantID, "token_env": source.TokenEnv, "git_url": r.HTTPSURL, "ssh_url": r.SSHURL, "transport": selected.Transport}
	for key, value := range provider {
		if value == "" {
			delete(provider, key)
		}
	}
	repo := domain.Repository{ID: selected.ID, Remotes: map[string]string{"origin": remote}, Base: r.DefaultBranch, Provider: provider}
	if err := ValidateImportedRepository(repo); err != nil {
		return selected, empty, err
	}
	selected.Source = source
	return selected, repo, nil
}

// ValidateImportedRepository checks only the new import snapshot; legacy
// catalog and journal metadata remains readable without these additive fields.
func ValidateImportedRepository(repo domain.Repository) error {
	if err := domain.ValidateID(repo.ID); err != nil {
		return err
	}
	p := repo.Provider
	if p["kind"] != "github" && p["kind"] != "bitbucket" && p["kind"] != "bitbucket-server" {
		return domain.E("invalid", "unsupported import provider")
	}
	if p["host"] == "" || strings.ContainsAny(p["host"], "/@?#\\") {
		return domain.E("invalid", "invalid import host")
	}
	if err := validateImportURL(p["api_url"], "https"); err != nil {
		return err
	}
	if p["repository"] == "" || p["remote_id"] == "" {
		return domain.E("invalid", "missing import provider identity")
	}
	for _, v := range p {
		if len(v) > 4096 || strings.ContainsFunc(v, unicode.IsControl) {
			return domain.E("invalid", "invalid import provider metadata")
		}
	}
	for _, part := range strings.Split(p["repository"], "/") {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\\?#@") {
			return domain.E("invalid", "invalid import repository coordinates")
		}
	}
	if len(strings.Split(p["repository"], "/")) != 2 {
		return domain.E("invalid", "invalid import repository coordinates")
	}
	if p["connection"] != "" {
		if err := validateImportConnection(p["connection"]); err != nil {
			return err
		}
		if p["grant_id"] == "" {
			return domain.E("invalid", "explicit connection requires the reviewed grant identity")
		}
	}
	if p["grant_id"] != "" && p["connection"] == "" {
		return domain.E("invalid", "grant identity requires an explicit connection")
	}
	if p["token_env"] != "" {
		for i, c := range p["token_env"] {
			if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_' || (i > 0 && c >= '0' && c <= '9')) {
				return domain.E("invalid", "invalid token environment variable")
			}
		}
	}
	if len(repo.Remotes) != 1 {
		return domain.E("invalid", "import snapshot must contain only origin")
	}
	if err := validateImportURL(repo.Remotes["origin"], p["transport"]); err != nil {
		return err
	}
	if p["git_url"] != "" {
		if err := validateImportURL(p["git_url"], "https"); err != nil {
			return err
		}
	}
	if p["ssh_url"] != "" {
		if err := validateImportURL(p["ssh_url"], "ssh"); err != nil {
			return err
		}
	}
	if repo.Base != "" {
		if err := domain.ValidateBranch(repo.Base); err != nil {
			return err
		}
	}
	return nil
}

func validateImportURL(raw, transport string) error {
	if err := domain.ValidateURL(raw); err != nil {
		return err
	}
	if strings.ContainsFunc(raw, unicode.IsControl) || strings.ContainsAny(raw, "\\?#") {
		return domain.E("invalid", "invalid import URL")
	}
	if transport == "ssh" && !strings.Contains(raw, "://") {
		authority, path, ok := strings.Cut(raw, ":")
		if !ok || authority == "" || path == "" || strings.ContainsAny(authority, "/") {
			return domain.E("invalid", "invalid SSH clone URL")
		}
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Scheme != transport || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return domain.E("invalid", "import URL must use %s without credentials, query or fragment", transport)
	}
	if u.User != nil {
		if _, password := u.User.Password(); password || transport != "ssh" {
			return domain.E("invalid", "credentials in import URL")
		}
	}
	escaped := strings.ToLower(u.EscapedPath())
	if strings.Contains(escaped, "%2f") || strings.Contains(escaped, "%5c") || strings.Contains(escaped, "%25") || strings.ContainsFunc(u.Path, unicode.IsControl) || strings.Contains(u.Path, "\\") {
		return domain.E("invalid", "unsafe encoded import URL path")
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == "." || part == ".." {
			return domain.E("invalid", "unsafe import URL path")
		}
	}
	return nil
}

func importConflict(c domain.Catalog, repo domain.Repository) (string, string) {
	ids := make([]string, 0, len(c.Repositories))
	for id := range c.Repositories {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		r := c.Repositories[id]
		if strings.EqualFold(id, repo.ID) || repositoriesEquivalent(r, repo) {
			return id, ""
		}
	}
	ids = ids[:0]
	for id := range c.Retired {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		r := c.Retired[id]
		if strings.EqualFold(id, repo.ID) || repositoriesEquivalent(r.Repository, repo) {
			return "", id
		}
	}
	return "", ""
}

func repositoriesEquivalent(a, b domain.Repository) bool {
	keys := repositoryIdentityKeys(a)
	for k := range repositoryIdentityKeys(b) {
		if keys[k] {
			return true
		}
	}
	return false
}

func repositoryIdentityKeys(r domain.Repository) map[string]bool {
	keys := map[string]bool{}
	p := r.Provider
	kind, host, api := p["kind"], strings.ToLower(p["host"]), strings.TrimRight(p["api_url"], "/")
	if kind != "" && host != "" {
		if api == "" {
			api = defaultImportAPI(kind, host)
		}
		prefix := strings.ToLower(kind + "|" + host + "|" + api + "|")
		if p["repository"] != "" {
			keys["coordinates:"+prefix+strings.ToLower(p["repository"])] = true
		}
		if p["remote_id"] != "" {
			keys["id:"+prefix+p["remote_id"]] = true
		}
	}
	for _, raw := range r.Remotes {
		host, path := importRemoteIdentity(raw)
		if host == "" || path == "" {
			continue
		}
		keys["url:"+host+"/"+path] = true
		if host == "github.com" || host == "bitbucket.org" {
			kind := "github"
			if host == "bitbucket.org" {
				kind = "bitbucket"
			}
			keys["coordinates:"+kind+"|"+host+"|"+defaultImportAPI(kind, host)+"|"+path] = true
		}
	}
	for _, raw := range []string{p["git_url"], p["ssh_url"]} {
		if raw != "" {
			host, path := importRemoteIdentity(raw)
			if host != "" && path != "" {
				keys["url:"+host+"/"+path] = true
			}
		}
	}
	return keys
}

func defaultImportAPI(kind, host string) string {
	if kind == "github" {
		if host == "github.com" {
			return "https://api.github.com"
		}
		return "https://" + host + "/api/v3"
	}
	if kind == "bitbucket" && host == "bitbucket.org" {
		return "https://api.bitbucket.org/2.0"
	}
	return "https://" + host + "/rest/api/1.0"
}

func importRemoteIdentity(raw string) (string, string) {
	var host, path string
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return "", ""
		}
		host = u.Host
		if (u.Scheme == "https" && u.Port() == "443") || (u.Scheme == "ssh" && u.Port() == "22") {
			host = u.Hostname()
		}
		path = u.Path
	} else {
		authority, p, ok := strings.Cut(raw, ":")
		if !ok || strings.ContainsAny(authority, "/\\") {
			return "", ""
		}
		if at := strings.LastIndex(authority, "@"); at >= 0 {
			authority = authority[at+1:]
		}
		host, path = authority, p
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	return strings.ToLower(host), strings.ToLower(path)
}

func importCredentialRequest(repo domain.Repository) domain.CredentialRequest {
	p := repo.Provider
	req := domain.CredentialRequest{Provider: p["kind"], Host: p["host"], APIURL: p["api_url"], Repository: p["repository"], Connection: p["connection"], GrantID: p["grant_id"], TokenEnv: p["token_env"]}
	if p["transport"] == "https" {
		req.GitURL = repo.Remotes["origin"]
	}
	return req
}

func validateImportConnection(name string) error {
	if name == "" || len(name) > 120 || strings.TrimSpace(name) != name || strings.ContainsAny(name, "/\\") || strings.ContainsFunc(name, unicode.IsControl) {
		return domain.E("invalid", "invalid saved connection name")
	}
	return nil
}

func (s *Service) validateImportCredentials(ctx context.Context, repo domain.Repository, allowRefresh bool) error {
	req := importCredentialRequest(repo)
	req.AllowRefresh = allowRefresh
	if req.Connection == "" && req.TokenEnv == "" {
		return ctx.Err()
	}
	if s.Credentials == nil {
		return domain.E("authentication_required", "selected credential source is unavailable")
	}
	_, err := s.Credentials.Resolve(ctx, req)
	return err
}

func (s *Service) importGit(repo domain.Repository) (Git, error) {
	if repo.Provider["transport"] == "https" && repo.Provider["token_env"] == "" && s.GitForConnection != nil {
		return s.GitForConnection(importCredentialRequest(repo))
	}
	return s.Git, nil
}

func (s *Service) applyImport(ctx context.Context, op *Operation) (Result, error) {
	r := NewResult("repo.import")
	r.OperationID = op.ID
	if len(op.Steps) == 0 {
		return s.finish(op, r, domain.E("invalid", "empty import journal"))
	}
	for _, step := range op.Steps {
		if err := ValidateImportStep(step); err != nil {
			return s.finish(op, r, err)
		}
		if err := s.validateImportCredentials(ctx, *step.ImportedRepository, true); err != nil {
			return s.finish(op, r, err)
		}
	}
	var failures []error
	for i := range op.Steps {
		if err := ctx.Err(); err != nil {
			return s.finish(op, r, errors.Join(append(failures, err)...))
		}
		step := &op.Steps[i]
		repo := *step.ImportedRepository
		path, err := s.repoPath(repo.ID)
		if err != nil {
			return s.finish(op, r, err)
		}
		c, rev, err := s.Store.LoadCatalog()
		if err != nil {
			return s.finish(op, r, err)
		}
		git, err := s.importGit(repo)
		if err != nil {
			return s.finish(op, r, err)
		}
		scoped := *s
		scoped.Git = git
		if registered, ok := c.Repositories[repo.ID]; ok {
			if !reflect.DeepEqual(registered, repo) && (step.PreviousImportedRepository == nil || !reflect.DeepEqual(registered, *step.PreviousImportedRepository)) {
				return s.finish(op, r, domain.E("conflict", "imported registration for %s changed", repo.ID))
			}
			if err := scoped.verifyRepositoryAt(ctx, repo, path); err != nil {
				return s.finish(op, r, err)
			}
			if err := s.bindImportStep(ctx, *step, path); err != nil {
				return s.finish(op, r, err)
			}
			if !reflect.DeepEqual(registered, repo) {
				c.Repositories[repo.ID] = repo
				if err := s.Store.SaveCatalog(c, rev); err != nil {
					return s.finish(op, r, err)
				}
			}
			step.State, step.Error = "succeeded", ""
			if err := s.Journal.Save(op); err != nil {
				return importDurabilityFailure(r, err)
			}
			r.Items = append(r.Items, Item{Repository: repo.ID, Path: path, RefFormat: step.RefFormat, Outcome: "unchanged"})
			continue
		}
		if step.State == "succeeded" {
			return s.finish(op, r, domain.E("conflict", "successful import %s is missing from the catalog", repo.ID))
		}
		active, retired := importConflict(c, repo)
		if active != "" || retired != "" {
			return s.finish(op, r, domain.E("conflict", "import identity %s is already registered or retired", repo.ID))
		}
		exists, err := s.Store.Exists("repos", repo.ID+".git")
		if err != nil {
			return s.finish(op, r, err)
		}
		if exists && step.State == "planned" {
			return s.finish(op, r, domain.E("conflict", "destination appeared before import %s started", repo.ID))
		}
		if exists {
			if err := scoped.verifyInterruptedClone(ctx, path, repo.Remotes["origin"]); err != nil {
				return s.finish(op, r, err)
			}
		}
		step.State, step.Error = "started", ""
		if err := s.Journal.Save(op); err != nil {
			return importDurabilityFailure(r, err)
		}
		if err := s.Store.EnsureDir("repos"); err != nil {
			return s.finish(op, r, err)
		}
		s.event(repo.ID, "running")
		err = scoped.transferImport(ctx, step, path, exists, op)
		if err == nil {
			err = scoped.verifyInterruptedClone(ctx, path, repo.Remotes["origin"])
		}
		if err != nil {
			// A refused journal write is shared durability failure; it must never
			// be treated as an independent failed Git transfer.
			var durable *importJournalError
			if errors.As(err, &durable) {
				return importDurabilityFailure(r, durable.err)
			}
			step.State, step.Error = "failed", err.Error()
			s.event(repo.ID, "failed")
			r.Items = append(r.Items, Item{Repository: repo.ID, Path: path, RefFormat: step.RefFormat, Outcome: "failed", Problem: ProblemFor(err)})
			failures = append(failures, err)
			if err := s.Journal.Save(op); err != nil {
				return importDurabilityFailure(r, err)
			}
			if ctx.Err() != nil {
				return s.finish(op, r, errors.Join(append(failures, ctx.Err())...))
			}
			continue
		}
		if err := s.bindImportStep(ctx, *step, path); err != nil {
			return s.finish(op, r, err)
		}
		c.Repositories[repo.ID] = repo
		if err := s.Store.SaveCatalog(c, rev); err != nil {
			return s.finish(op, r, err)
		}
		step.State = "succeeded"
		if err := s.Journal.Save(op); err != nil {
			return importDurabilityFailure(r, err)
		}
		s.event(repo.ID, "succeeded")
		r.Items = append(r.Items, Item{Repository: repo.ID, Path: path, RefFormat: step.RefFormat, Outcome: "created"})
	}
	return s.finish(op, r, errors.Join(failures...))
}

func importDurabilityFailure(r Result, err error) (Result, error) {
	r.Outcome = "partial"
	r.Problem = ProblemFor(err)
	return r, err
}

type importJournalError struct{ err error }

func (e *importJournalError) Error() string { return e.err.Error() }
func (e *importJournalError) Unwrap() error { return e.err }

func (s *Service) transferImport(ctx context.Context, step *Step, path string, exists bool, op *Operation) error {
	remote := step.ImportedRepository.Remotes["origin"]
	planner, canPlan := s.Git.(ClonePlanner)
	if step.RefFormat == "" || (exists && canPlan && step.RefFormat == "files" && step.RequestedRefFormat != "files") {
		if canPlan {
			format, err := planner.PlanClone(ctx, remote, path, step.RequestedRefFormat)
			if err != nil {
				return err
			}
			if format != "files" && format != "reftable" {
				return domain.E("invalid", "clone planner returned invalid ref format")
			}
			step.RefFormat = format
		} else {
			if step.RequestedRefFormat != "" && step.RequestedRefFormat != "auto" {
				return domain.E("unavailable", "Git adapter does not support explicit ref format selection")
			}
			step.RefFormat = "files"
		}
		if err := s.Journal.Save(op); err != nil {
			return &importJournalError{err}
		}
	}
	if exists {
		if canPlan {
			return planner.InitializeWithFormat(ctx, path, step.RefFormat)
		}
		return s.Git.Initialize(ctx, path)
	}
	if canPlan {
		return planner.CloneWithFormat(ctx, remote, path, step.RefFormat)
	}
	return s.Git.Clone(ctx, remote, path)
}

func (s *Service) bindImport(ctx context.Context, repo domain.Repository, path string) error {
	if repo.Provider["transport"] == "https" && repo.Provider["connection"] != "" && repo.Provider["token_env"] == "" {
		if s.BindConnection == nil {
			return domain.E("unavailable", "persistent Git connection binding is unavailable")
		}
		return s.BindConnection(ctx, path, importCredentialRequest(repo))
	}
	return nil
}
