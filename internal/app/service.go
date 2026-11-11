// Package app implements repository/workspace workflows independently of presentation.
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"time"

	"bearforge/internal/domain"
)

type IntentStore interface {
	RootPath() string
	SafePath(...string) (string, error)
	Exists(...string) (bool, error)
	EnsureDir(...string) error
	CreateDir(...string) error
	RemoveEmpty(...string) error
	LoadCatalog() (domain.Catalog, string, error)
	SaveCatalog(domain.Catalog, string) error
	LoadWorkspace(string) (domain.Workspace, string, error)
	SaveWorkspace(domain.Workspace, string) error
	DeleteWorkspace(string, string) error
	ListWorkspaces() ([]string, error)
}

type Git interface {
	Clone(context.Context, string, string) error
	Initialize(context.Context, string) error
	VerifyBare(context.Context, string) error
	Config(context.Context, string, string) (string, error)
	Fetch(context.Context, string) error
	Resolve(context.Context, string, string) (string, error)
	BranchExists(context.Context, string, string) (bool, error)
	RefExists(context.Context, string, string) (bool, error)
	ValidateBranch(context.Context, string) error
	DefaultBase(context.Context, string, string) (string, error)
	Worktrees(context.Context, string) ([]domain.Worktree, error)
	VerifyCommon(context.Context, string, string) error
	Add(context.Context, string, string, string, string, bool) error
	SetUpstream(context.Context, string, string, string) error
	HasSubmodules(context.Context, string, string) (bool, error)
	Inspect(context.Context, string) (domain.Status, error)
	CheckRemoval(context.Context, string, string) error
	PreserveHistory(context.Context, string, string, string, string) error
	Remove(context.Context, string, string) error
}

type Journal interface {
	Lock(context.Context) (func() error, error)
	Save(*Operation) error
	Load(string) (*Operation, error)
	List() ([]Operation, error)
}

type Operation struct {
	Version       int       `json:"schema_version"`
	ID            string    `json:"id"`
	Root          string    `json:"root"`
	Kind          string    `json:"kind"`
	Workspace     string    `json:"workspace,omitempty"`
	DefaultBranch string    `json:"default_branch,omitempty"`
	Remote        string    `json:"remote,omitempty"`
	State         string    `json:"state"`
	Created       time.Time `json:"created"`
	Steps         []Step    `json:"steps"`
	Error         string    `json:"error,omitempty"`
}

type Step struct {
	Repository string `json:"repository"`
	Branch     string `json:"branch,omitempty"`
	Base       string `json:"base,omitempty"`
	Tracking   string `json:"tracking,omitempty"`
	Start      string `json:"start,omitempty"`
	Upstream   string `json:"upstream,omitempty"`
	Create     bool   `json:"create"`
	State      string `json:"state"`
	Error      string `json:"error,omitempty"`
}

type Problem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type Item struct {
	Repository     string         `json:"repository,omitempty"`
	Workspace      string         `json:"workspace,omitempty"`
	Path           string         `json:"path,omitempty"`
	Branch         string         `json:"branch,omitempty"`
	ExpectedBranch string         `json:"expected_branch,omitempty"`
	Outcome        string         `json:"outcome"`
	Status         *domain.Status `json:"status,omitempty"`
	Problem        *Problem       `json:"error,omitempty"`
}
type Result struct {
	Version     int      `json:"schema_version"`
	Command     string   `json:"command"`
	OperationID string   `json:"operation_id,omitempty"`
	Outcome     string   `json:"outcome"`
	Items       []Item   `json:"results"`
	PlanDigest  string   `json:"plan_digest,omitempty"`
	Data        any      `json:"data,omitempty"`
	Problem     *Problem `json:"error,omitempty"`
}
type Event struct {
	Repository string `json:"repository,omitempty"`
	State      string `json:"state"`
}
type Service struct {
	Store   IntentStore
	Git     Git
	Journal Journal
	Observe func(Event)
}

func New(store IntentStore, git Git, journal Journal) *Service {
	return &Service{Store: store, Git: git, Journal: journal}
}
func NewResult(command string) Result {
	return Result{Version: 1, Command: command, Outcome: "success", Items: []Item{}}
}
func ProblemFor(err error) *Problem {
	if err == nil {
		return nil
	}
	code := "failed"
	var e *domain.Error
	if errors.As(err, &e) {
		code = e.Code
	}
	if errors.Is(err, context.Canceled) {
		code = "cancelled"
	}
	return &Problem{Code: code, Message: err.Error()}
}
func (s *Service) event(repo, state string) {
	if s.Observe != nil {
		s.Observe(Event{Repository: repo, State: state})
	}
}
func newOperation(root, kind, workspace string) *Operation {
	var b [16]byte
	_, err := rand.Read(b[:])
	if err != nil {
		panic(err)
	}
	return &Operation{Version: 1, ID: hex.EncodeToString(b[:]), Root: root, Kind: kind, Workspace: workspace, State: "running", Created: time.Now().UTC(), Steps: []Step{}}
}

func (s *Service) lock(ctx context.Context, resumeID string) (func() error, error) {
	release, err := s.Journal.Lock(ctx)
	if err != nil {
		return nil, err
	}
	ops, err := s.Journal.List()
	if err != nil {
		release()
		return nil, err
	}
	for _, op := range ops {
		if op.ID != resumeID && op.State != "complete" && op.State != "abandoned" {
			release()
			return nil, domain.E("conflict", "operation %s is unfinished; inspect and resume it first", op.ID)
		}
	}
	return release, nil
}
func (s *Service) finish(op *Operation, result Result, err error) (Result, error) {
	result.OperationID = op.ID
	op.Error = ""
	if err != nil {
		op.State = "partial"
		op.Error = err.Error()
		result.Outcome = "partial"
		result.Problem = ProblemFor(err)
	} else {
		op.State = "complete"
	}
	if saveErr := s.Journal.Save(op); saveErr != nil {
		result.Outcome = "partial"
		err = errors.Join(err, saveErr)
		result.Problem = ProblemFor(err)
	}
	return result, err
}
func unique(names []string) []string {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	out := make([]string, 0, len(m))
	for n := range m {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
func (s *Service) repoPath(id string) (string, error) {
	if err := domain.ValidateID(id); err != nil {
		return "", err
	}
	return s.Store.SafePath("repos", id+".git")
}
func (s *Service) treePath(workspace, id string) (string, error) {
	if err := domain.ValidateID(workspace); err != nil {
		return "", err
	}
	if err := domain.ValidateID(id); err != nil {
		return "", err
	}
	return s.Store.SafePath(workspace, id)
}
func (s *Service) checkRepository(ctx context.Context, r domain.Repository) (string, error) {
	p, err := s.repoPath(r.ID)
	if err != nil {
		return "", err
	}
	if err := s.Git.VerifyBare(ctx, p); err != nil {
		return "", err
	}
	for name, want := range r.Remotes {
		got, err := s.Git.Config(ctx, p, "remote."+name+".url")
		if err != nil {
			return "", err
		}
		if got != want {
			return "", domain.E("conflict", "remote %s for %s differs from the catalog", name, r.ID)
		}
	}
	return p, nil
}
