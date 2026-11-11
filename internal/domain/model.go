// Package domain defines Bearforge's intent and observed Git state.
package domain

import "fmt"

type DirectoryEntry struct {
	Name      string
	Directory bool
	Link      bool
}

type Repository struct {
	ID       string
	Remotes  map[string]string
	Base     string
	Provider map[string]string
}

type Catalog struct {
	Repositories map[string]Repository
	Retired      map[string]RetiredRepository
	Remote       string
	Jobs         int
}

type RetiredRepository struct {
	Repository  Repository
	Operation   string
	Fingerprint string
}

type Mapping struct {
	Branch   string
	Base     string
	Tracking string
	PRTarget string
}

type Workspace struct {
	Name          string
	DefaultBranch string
	Repositories  map[string]Mapping
}

type Worktree struct {
	Path     string
	Head     string
	Branch   string
	Bare     bool
	Locked   bool
	Prunable bool
}

type Status struct {
	Path         string `json:"path"`
	Branch       string `json:"branch"`
	Head         string `json:"head"`
	Upstream     string `json:"upstream,omitempty"`
	Ahead        *int   `json:"ahead,omitempty"`
	Behind       *int   `json:"behind,omitempty"`
	Staged       int    `json:"staged"`
	Modified     int    `json:"modified"`
	Untracked    int    `json:"untracked"`
	Conflicts    int    `json:"conflicts"`
	Detached     bool   `json:"detached"`
	Unborn       bool   `json:"unborn"`
	UpstreamGone bool   `json:"upstream_gone"`
}

func (s Status) Dirty() bool { return s.Staged+s.Modified+s.Untracked+s.Conflicts > 0 }

type Error struct {
	Code    string
	Message string
	Cause   error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Cause }
func E(code, format string, args ...any) error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}
