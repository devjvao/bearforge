// Package operation persists resumable operation snapshots and provides a root lock.
package operation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gofrs/flock"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/config"
	"github.com/devjvao/bearforge/internal/domain"
)

const maxDocument = 2 << 20

type Store struct {
	files    *config.Store
	terminal atomic.Pointer[terminalIndex]
}

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
	limit := maxDocument
	if op.State != "complete" && op.State != "abandoned" {
		// A refused later snapshot must still be recoverable by abandonment.
		limit -= max(0, len("abandoned")-len(op.State))
	}
	if len(b)+1 > limit {
		// Keep full diagnostics in the caller's result, but never replace a
		// readable journal with an oversized snapshot of repeated failures.
		snapshot := *op
		snapshot.Steps = slices.Clone(op.Steps)
		snapshot.Error = journalDiagnostic(snapshot.Error)
		for i := range snapshot.Steps {
			snapshot.Steps[i].Error = journalDiagnostic(snapshot.Steps[i].Error)
		}
		b, err = json.MarshalIndent(&snapshot, "", "  ")
		if err != nil {
			return err
		}
		if len(b)+1 > limit {
			return domain.E("invalid", "operation exceeds 2 MiB")
		}
	}
	return s.files.Write([]string{".bearforge", "state", "operations", op.ID + ".json"}, append(b, '\n'))
}

func journalDiagnostic(message string) string {
	const limit = 4 << 10
	if len(message) <= limit {
		return message
	}
	return strings.ToValidUTF8(message[:limit], "\uFFFD") + "\n[diagnostic truncated]"
}

func (s *Store) open(id string) (*os.File, error) {
	if err := domain.ValidateID(id); err != nil {
		return nil, err
	}
	p, err := s.files.SafePath(".bearforge", "state", "operations", id+".json")
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

func (s *Store) Load(id string) (*app.Operation, error) {
	f, err := s.open(id)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := &io.LimitedReader{R: f, N: maxDocument + 1}
	op, err := decodeOperation(id, s.files.Root, r)
	if r.N == 0 {
		return nil, domain.E("invalid", "operation exceeds 2 MiB")
	}
	return op, err
}

func decodeOperation(id, root string, r io.Reader) (*app.Operation, error) {
	d := json.NewDecoder(r)
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
	// Older journals retain the root spelling supplied when they were created.
	if op.Root != root && sameDirectoryEntries(op.Root, root) {
		op.Root = root
	}
	if err := valid(&op, root); err != nil {
		return nil, err
	}
	return &op, nil
}

// Compare each differing name within the store's known parent. This also
// recognizes Windows DOS short names without following recorded ancestors.
func sameDirectoryEntries(recorded, current string) bool {
	if !filepath.IsAbs(recorded) || filepath.Clean(recorded) != recorded ||
		!strings.EqualFold(filepath.VolumeName(recorded), filepath.VolumeName(current)) ||
		strings.Count(recorded, string(filepath.Separator)) != strings.Count(current, string(filepath.Separator)) {
		return false
	}
	if runtime.GOOS != "windows" && !strings.EqualFold(recorded, current) {
		return false
	}
	for recorded != current {
		parent, recordedParent := filepath.Dir(current), filepath.Dir(recorded)
		if parent == current || recordedParent == recorded {
			return strings.EqualFold(current, recorded)
		}
		name := filepath.Base(recorded)
		if name != filepath.Base(current) {
			if !filepath.IsLocal(name) {
				return false
			}
			recordedInfo, err := os.Lstat(filepath.Join(parent, name))
			if err != nil || !recordedInfo.IsDir() {
				return false
			}
			currentInfo, err := os.Lstat(current)
			if err != nil || !os.SameFile(recordedInfo, currentInfo) {
				return false
			}
		}
		current, recorded = parent, recordedParent
	}
	return true
}

func (s *Store) operationIDs() ([]string, error) {
	p, err := s.files.SafePath(".bearforge", "state", "operations")
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			ids = append(ids, strings.TrimSuffix(entry.Name(), ".json"))
		}
	}
	return ids, nil
}

func (s *Store) List() ([]app.Operation, error) {
	ids, err := s.operationIDs()
	if err != nil {
		return nil, err
	}
	ops := []app.Operation{}
	var failures []error
	for _, id := range ids {
		op, err := s.Load(id)
		if err != nil {
			failures = append(failures, fmt.Errorf("operation %s.json: %w", id, err))
			continue
		}
		ops = append(ops, *op)
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
	if op.Kind == "repo.import" && len(op.Steps) == 0 {
		return domain.E("invalid", "empty import journal")
	}
	importIDs := map[string]bool{}
	for _, step := range op.Steps {
		if op.Kind == "repo.import" || step.ImportedRepository != nil || step.PreviousImportedRepository != nil || len(step.ImportBindingHistory) > 0 {
			if op.Kind != "repo.import" || step.ImportedRepository == nil || step.ImportedRepository.ID != step.Repository {
				return domain.E("invalid", "invalid import snapshot")
			}
			if err := app.ValidateImportStep(step); err != nil {
				return err
			}
			if importIDs[step.Repository] {
				return domain.E("invalid", "duplicate import repository")
			}
			importIDs[step.Repository] = true
		}
		if pin := step.RemoteBranch; pin != nil {
			if op.Kind != "workspace.add" || step.Branch == "" || domain.ValidateRemoteName(pin.Remote) != nil || domain.ValidateURL(pin.URL) != nil {
				return domain.E("invalid", "invalid remote identity in workspace branch plan")
			}
			if !domain.ValidCommitID(step.Start) || pin.OID != "" && !domain.ValidCommitID(pin.OID) {
				return domain.E("invalid", "invalid commit in workspace branch plan")
			}
			if step.Create && pin.OID != "" && step.Start != pin.OID {
				return domain.E("invalid", "workspace branch creation differs from its advertised commit")
			}
			if pin.LocalOID != "" && (!domain.ValidCommitID(pin.LocalOID) || step.Create || pin.OID == "" || step.Start != pin.OID && step.Start != pin.LocalOID) {
				return domain.E("invalid", "invalid local tip in workspace branch plan")
			}
			if step.Upstream != "" && (pin.OID == "" || !step.Create || step.Upstream != pin.Remote+"/"+step.Branch) {
				return domain.E("invalid", "invalid upstream in workspace branch plan")
			}
		}
		if step.RefFormat != "" && step.RefFormat != "files" && step.RefFormat != "reftable" {
			return domain.E("invalid", "invalid selected ref format")
		}
		if err := app.ValidateRefFormat(step.RequestedRefFormat); err != nil {
			return err
		}
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
