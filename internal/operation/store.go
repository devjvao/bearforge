// Package operation persists resumable operation snapshots and provides a root lock.
package operation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"bearforge/internal/app"
	"bearforge/internal/config"
	"bearforge/internal/domain"
)

type Store struct{ files *config.Store }

func New(files *config.Store) *Store { return &Store{files: files} }
func (s *Store) Lock(ctx context.Context) (func() error, error) {
	if err := s.files.EnsureDir(".bearforge", "state"); err != nil {
		return nil, err
	}
	path, err := s.files.SafePath(".bearforge", "state", "mutation.lock")
	if err != nil {
		return nil, err
	}
	lock := flock.New(path)
	wait, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ok, err := lock.TryLockContext(wait, 50*time.Millisecond)
	if err != nil || !ok {
		lock.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, domain.E("conflict", "another Bearforge command holds the mutation lock")
	}
	return lock.Close, nil
}
func (s *Store) Save(op *app.Operation) error {
	if err := valid(op, s.files.Root); err != nil {
		return err
	}
	b, err := json.MarshalIndent(op, "", "  ")
	if err != nil {
		return err
	}
	return s.files.Write([]string{".bearforge", "state", "operations", op.ID + ".json"}, append(b, '\n'))
}
func (s *Store) Load(id string) (*app.Operation, error) {
	if err := domain.ValidateID(id); err != nil {
		return nil, err
	}
	p, err := s.files.SafePath(".bearforge", "state", "operations", id+".json")
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 2<<20))
	d.DisallowUnknownFields()
	var op app.Operation
	if err := d.Decode(&op); err != nil {
		return nil, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, domain.E("invalid", "invalid operation document")
	}
	if op.ID != id {
		return nil, domain.E("invalid", "operation identity mismatch")
	}
	if err := valid(&op, s.files.Root); err != nil {
		return nil, err
	}
	return &op, nil
}
func (s *Store) List() ([]app.Operation, error) {
	p, err := s.files.SafePath(".bearforge", "state", "operations")
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(p)
	if os.IsNotExist(err) {
		return []app.Operation{}, nil
	}
	if err != nil {
		return nil, err
	}
	ops := []app.Operation{}
	var failures []error
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			op, err := s.Load(strings.TrimSuffix(entry.Name(), ".json"))
			if err != nil {
				failures = append(failures, fmt.Errorf("operation %s: %w", entry.Name(), err))
				continue
			}
			ops = append(ops, *op)
		}
	}
	return ops, errors.Join(failures...)
}
func valid(op *app.Operation, root string) error {
	if op.Version != 1 || op.Root != root {
		return domain.E("invalid", "operation schema/root mismatch")
	}
	if err := domain.ValidateID(op.ID); err != nil {
		return err
	}
	if op.Workspace != "" {
		if err := domain.ValidateID(op.Workspace); err != nil {
			return err
		}
	}
	if op.Remote != "" {
		if err := domain.ValidateURL(op.Remote); err != nil {
			return err
		}
	}
	for _, step := range op.Steps {
		if err := domain.ValidateID(step.Repository); err != nil {
			return err
		}
		if step.Branch != "" {
			if err := domain.ValidateBranch(step.Branch); err != nil {
				return err
			}
		}
	}
	return nil
}
