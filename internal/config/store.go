// Package config stores versioned intent without inspecting Git metadata.
package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"bearforge/internal/domain"
)

const maxDocument = 2 << 20

type Store struct{ Root string }

func (s *Store) RootPath() string { return s.Root }
func (s *Store) CreateDir(parts ...string) error {
	p, err := s.SafePath(parts...)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	return os.Mkdir(p, 0700)
}
func (s *Store) Exists(parts ...string) (bool, error) {
	p, err := s.SafePath(parts...)
	if err != nil {
		return false, err
	}
	_, err = os.Lstat(p)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}
func (s *Store) EnsureDir(parts ...string) error {
	p, err := s.SafePath(parts...)
	if err != nil {
		return err
	}
	return os.MkdirAll(p, 0700)
}
func (s *Store) RemoveEmpty(parts ...string) error {
	p, err := s.SafePath(parts...)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

type defaultsDTO struct {
	Remote string `yaml:"remote"`
	Jobs   int    `yaml:"jobs"`
}
type repoDTO struct {
	Remotes  map[string]string `yaml:"remotes"`
	Base     string            `yaml:"base,omitempty"`
	Provider map[string]string `yaml:"provider,omitempty"`
}
type catalogDTO struct {
	Version      int                   `yaml:"schema_version"`
	Defaults     defaultsDTO           `yaml:"defaults"`
	Repositories map[string]repoDTO    `yaml:"repositories"`
	Retired      map[string]retiredDTO `yaml:"retired,omitempty"`
	Extensions   map[string]any        `yaml:"extensions,omitempty"`
}

type retiredDTO struct {
	Repository  repoDTO `yaml:"repository"`
	Operation   string  `yaml:"operation"`
	Fingerprint string  `yaml:"fingerprint"`
}
type mappingDTO struct {
	Branch   string `yaml:"branch"`
	Base     string `yaml:"base,omitempty"`
	Tracking string `yaml:"tracking,omitempty"`
	PRTarget string `yaml:"pr_target,omitempty"`
}
type workspaceDTO struct {
	Version       int                   `yaml:"schema_version"`
	Name          string                `yaml:"name"`
	DefaultBranch string                `yaml:"default_branch"`
	Repositories  map[string]mappingDTO `yaml:"repositories"`
	Extensions    map[string]any        `yaml:"extensions,omitempty"`
}

func New(root string) *Store {
	absolute, err := filepath.Abs(root)
	if err == nil {
		root = absolute
	}
	return &Store{Root: filepath.Clean(root)}
}
func (s *Store) CatalogPath() string { return filepath.Join(s.Root, ".bearforge", "catalog.yaml") }
func Revision(b []byte) string       { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// SafePath refuses traversal and any existing link/reparse point along the path.
func (s *Store) SafePath(parts ...string) (string, error) {
	for _, p := range parts {
		if filepath.IsAbs(p) {
			return "", domain.E("unsafe_path", "absolute child path refused")
		}
		for _, segment := range strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' }) {
			if segment == ".." {
				return "", domain.E("unsafe_path", "parent traversal refused")
			}
		}
	}
	p := filepath.Join(append([]string{s.Root}, parts...)...)
	rel, err := filepath.Rel(s.Root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", domain.E("unsafe_path", "path is outside root")
	}
	for current := p; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		if err == nil && isLink(info) {
			return "", domain.E("unsafe_path", "link or reparse point refused: %s", current)
		}
		parent := filepath.Dir(current)
		if current == parent {
			break
		}
	}
	return p, nil
}

func read(path string) ([]byte, string, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxDocument+1))
	if err != nil {
		return nil, "", err
	}
	if len(b) > maxDocument {
		return nil, "", domain.E("invalid", "configuration exceeds 2 MiB")
	}
	return b, Revision(b), nil
}

func decode(b []byte, target any) error {
	var node yaml.Node
	if err := yaml.Unmarshal(b, &node); err != nil {
		return domain.E("invalid", "invalid YAML: %v", err)
	}
	if err := validateNode(&node, 0); err != nil {
		return err
	}
	d := yaml.NewDecoder(bytes.NewReader(b))
	d.KnownFields(true)
	if err := d.Decode(target); err != nil {
		return domain.E("invalid", "invalid configuration: %v", err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return domain.E("invalid", "configuration must contain exactly one YAML document")
	}
	return nil
}

func validateNode(n *yaml.Node, depth int) error {
	if depth > 32 || n.Kind == yaml.AliasNode || n.Anchor != "" || n.Tag == "!!merge" {
		return domain.E("invalid", "YAML aliases, anchors, merge keys or excessive nesting are not supported")
	}
	if n.Tag != "" && !strings.HasPrefix(n.Tag, "!!") {
		return domain.E("invalid", "custom YAML tags are not supported")
	}
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i < len(n.Content); i += 2 {
			key := n.Content[i]
			if key.Kind != yaml.ScalarNode || seen[key.Value] {
				return domain.E("invalid", "duplicate or invalid YAML key %q", key.Value)
			}
			seen[key.Value] = true
		}
	}
	for _, child := range n.Content {
		if err := validateNode(child, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) LoadCatalog() (domain.Catalog, string, error) {
	c := domain.Catalog{Repositories: map[string]domain.Repository{}, Retired: map[string]domain.RetiredRepository{}, Remote: "origin", Jobs: 4}
	p, err := s.SafePath(".bearforge", "catalog.yaml")
	if err != nil {
		return c, "", err
	}
	b, rev, err := read(p)
	if err != nil || rev == "" {
		return c, rev, err
	}
	var dto catalogDTO
	if err := decode(b, &dto); err != nil {
		return c, rev, err
	}
	if dto.Version != 1 && dto.Version != 2 {
		return c, rev, domain.E("invalid", "unsupported catalog schema %d", dto.Version)
	}
	if dto.Version == 1 && dto.Retired != nil {
		return c, rev, domain.E("invalid", "retired identities require catalog schema 2")
	}
	if dto.Defaults.Remote != "" {
		c.Remote = dto.Defaults.Remote
	}
	if dto.Defaults.Jobs != 0 {
		c.Jobs = dto.Defaults.Jobs
	}
	if err := domain.ValidateRemoteName(c.Remote); err != nil {
		return c, rev, err
	}
	if c.Jobs < 1 || c.Jobs > 64 {
		return c, rev, domain.E("invalid", "jobs must be between 1 and 64")
	}
	for id, r := range dto.Repositories {
		repository, err := decodeRepository(id, r)
		if err != nil {
			return c, rev, err
		}
		c.Repositories[id] = repository
	}
	for id, r := range dto.Retired {
		if _, ok := c.Repositories[id]; ok {
			return c, rev, domain.E("invalid", "repository %s is both active and retired", id)
		}
		repository, err := decodeRepository(id, r.Repository)
		if err != nil {
			return c, rev, err
		}
		if err := domain.ValidateID(r.Operation); err != nil {
			return c, rev, err
		}
		if !validRevision(r.Fingerprint) {
			return c, rev, domain.E("invalid", "invalid retired repository fingerprint")
		}
		c.Retired[id] = domain.RetiredRepository{Repository: repository, Operation: r.Operation, Fingerprint: r.Fingerprint}
	}
	return c, rev, nil
}

func (s *Store) SaveCatalog(c domain.Catalog, rev string) error {
	version := 2
	if rev != "" {
		var err error
		version, err = s.CatalogVersion()
		if err != nil {
			return err
		}
	}
	if version == 1 && len(c.Retired) > 0 {
		return domain.E("migration_required", "run config migrate --to 2 before storing retired identities")
	}
	d := catalogDTO{Version: version, Defaults: defaultsDTO{Remote: c.Remote, Jobs: c.Jobs}, Repositories: map[string]repoDTO{}, Retired: map[string]retiredDTO{}}
	for id, r := range c.Repositories {
		d.Repositories[id] = repoDTO{Remotes: r.Remotes, Base: r.Base, Provider: r.Provider}
	}
	for id, r := range c.Retired {
		d.Retired[id] = retiredDTO{Repository: repoDTO{Remotes: r.Repository.Remotes, Base: r.Repository.Base, Provider: r.Repository.Provider}, Operation: r.Operation, Fingerprint: r.Fingerprint}
	}
	return s.save([]string{".bearforge", "catalog.yaml"}, d, rev)
}

func decodeRepository(id string, r repoDTO) (domain.Repository, error) {
	if err := domain.ValidateID(id); err != nil {
		return domain.Repository{}, err
	}
	if len(r.Remotes) == 0 {
		return domain.Repository{}, domain.E("invalid", "repository %s has no remotes", id)
	}
	for name, u := range r.Remotes {
		if err := domain.ValidateRemoteName(name); err != nil {
			return domain.Repository{}, err
		}
		if err := domain.ValidateURL(u); err != nil {
			return domain.Repository{}, err
		}
	}
	return domain.Repository{ID: id, Remotes: r.Remotes, Base: r.Base, Provider: r.Provider}, nil
}

func (s *Store) LoadWorkspace(name string) (domain.Workspace, string, error) {
	w := domain.Workspace{Name: name, DefaultBranch: name, Repositories: map[string]domain.Mapping{}}
	if err := domain.ValidateID(name); err != nil {
		return w, "", err
	}
	p, err := s.SafePath(".bearforge", "workspaces", name+".yaml")
	if err != nil {
		return w, "", err
	}
	b, rev, err := read(p)
	if err != nil {
		return w, rev, err
	}
	if rev == "" {
		return w, "", domain.E("not_found", "workspace %s is not registered", name)
	}
	var d workspaceDTO
	if err := decode(b, &d); err != nil {
		return w, rev, err
	}
	if d.Version != 1 || d.Name != name {
		return w, rev, domain.E("invalid", "unsupported schema or workspace identity mismatch")
	}
	if err := domain.ValidateBranch(d.DefaultBranch); err != nil {
		return w, rev, err
	}
	w.DefaultBranch = d.DefaultBranch
	for id, m := range d.Repositories {
		if err := domain.ValidateID(id); err != nil {
			return w, rev, err
		}
		if err := domain.ValidateBranch(m.Branch); err != nil {
			return w, rev, err
		}
		if m.Tracking != "" && m.Tracking != "none" && m.Tracking != "same-name" && m.Tracking != "preserve" {
			return w, rev, domain.E("invalid", "invalid tracking policy")
		}
		w.Repositories[id] = domain.Mapping{Branch: m.Branch, Base: m.Base, Tracking: m.Tracking, PRTarget: m.PRTarget}
	}
	return w, rev, nil
}

func (s *Store) SaveWorkspace(w domain.Workspace, rev string) error {
	if err := domain.ValidateID(w.Name); err != nil {
		return err
	}
	d := workspaceDTO{Version: 1, Name: w.Name, DefaultBranch: w.DefaultBranch, Repositories: map[string]mappingDTO{}}
	for id, m := range w.Repositories {
		d.Repositories[id] = mappingDTO{Branch: m.Branch, Base: m.Base, Tracking: m.Tracking, PRTarget: m.PRTarget}
	}
	return s.save([]string{".bearforge", "workspaces", w.Name + ".yaml"}, d, rev)
}

func (s *Store) ListWorkspaces() ([]string, error) {
	p, err := s.SafePath(".bearforge", "workspaces")
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(p)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") {
			name := strings.TrimSuffix(e.Name(), ".yaml")
			if err := domain.ValidateID(name); err != nil {
				return nil, err
			}
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

func (s *Store) DeleteWorkspace(name, rev string) error {
	if err := domain.ValidateID(name); err != nil {
		return err
	}
	p, err := s.SafePath(".bearforge", "workspaces", name+".yaml")
	if err != nil {
		return err
	}
	_, current, err := read(p)
	if err != nil {
		return err
	}
	if current != rev {
		return domain.E("conflict", "workspace changed; retry")
	}
	return os.Remove(p)
}

func (s *Store) save(parts []string, value any, rev string) error {
	p, err := s.SafePath(parts...)
	if err != nil {
		return err
	}
	old, current, err := read(p)
	if err != nil {
		return err
	}
	if current != rev {
		return domain.E("conflict", "configuration changed; reload and retry")
	}
	var next yaml.Node
	if err := next.Encode(value); err != nil {
		return err
	}
	if len(old) > 0 {
		var previous yaml.Node
		if err := yaml.Unmarshal(old, &previous); err != nil {
			return err
		}
		mergeComments(&next, &previous)
	}
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(&next); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return s.Write(parts, b.Bytes())
}

func mergeComments(next, previous *yaml.Node) {
	if previous.Kind == yaml.DocumentNode && next.Kind != yaml.DocumentNode && len(previous.Content) == 1 {
		previous = previous.Content[0]
	}
	if next.Kind == yaml.DocumentNode && previous.Kind != yaml.DocumentNode && len(next.Content) == 1 {
		next = next.Content[0]
	}
	next.HeadComment = previous.HeadComment
	next.LineComment = previous.LineComment
	next.FootComment = previous.FootComment
	if next.Kind == yaml.MappingNode && previous.Kind == yaml.MappingNode {
		for i := 0; i < len(previous.Content); i += 2 {
			key, val := previous.Content[i], previous.Content[i+1]
			found := false
			for j := 0; j < len(next.Content); j += 2 {
				if next.Content[j].Value == key.Value {
					mergeComments(next.Content[j], key)
					mergeComments(next.Content[j+1], val)
					found = true
					break
				}
			}
			if !found && key.Value == "extensions" {
				next.Content = append(next.Content, key, val)
			}
		}
	} else if next.Kind == yaml.DocumentNode && previous.Kind == yaml.DocumentNode && len(next.Content) == 1 && len(previous.Content) == 1 {
		mergeComments(next.Content[0], previous.Content[0])
	}
}

// Write durably replaces a Bearforge-owned file, never a Git file.
func (s *Store) Write(parts []string, b []byte) error {
	p, err := s.SafePath(parts...)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	if _, err := s.SafePath(parts...); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".bearforge-write-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := replaceFile(tmp, p); err != nil {
		return fmt.Errorf("replace configuration: %w", err)
	}
	return syncDirectory(filepath.Dir(p))
}
