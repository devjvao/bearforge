package operation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/domain"
)

const maxTerminalRevisions = 2048

// A published index is immutable. Concurrent scans can share it, and a scan
// finishing out of order only changes which revisions the next scan can reuse.
type terminalIndex struct {
	root string
	byID map[string][sha256.Size]byte
}

// PendingSummaries returns unfinished operations in ID order and reports each
// unreadable journal. Only strictly validated terminal revisions skip decoding.
func (s *Store) PendingSummaries(ctx context.Context) ([]app.OperationSummary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ids, err := s.operationIDs()
	if err != nil {
		return nil, err
	}
	root := s.files.Root
	var previous map[string][sha256.Size]byte
	if index := s.terminal.Load(); index != nil && index.root == root {
		previous = index.byID
	}
	next := make(map[string][sha256.Size]byte, min(len(ids), maxTerminalRevisions))
	var pending []app.OperationSummary
	var failures []error
	var document bytes.Buffer
	for _, id := range ids {
		if ctx.Err() != nil {
			break
		}
		if err := s.readDocument(id, &document); err != nil {
			failures = append(failures, fmt.Errorf("operation %s.json: %w", id, err))
			continue
		}
		digest := sha256.Sum256(document.Bytes())
		if known, ok := previous[id]; !ok || known != digest {
			op, err := decodeOperation(id, root, &document)
			if err != nil {
				failures = append(failures, fmt.Errorf("operation %s.json: %w", id, err))
				continue
			}
			if summary, ok := op.PendingSummary(); ok {
				pending = append(pending, summary)
				continue
			}
		}
		// Retaining a stable subset also yields cache hits when a history is
		// larger than the bound. Deleted and changed entries drop out naturally.
		if len(next) < maxTerminalRevisions {
			next[id] = digest
		}
	}
	if err := ctx.Err(); err != nil {
		failures = append(failures, err)
	} else {
		s.terminal.Store(&terminalIndex{root: root, byID: next})
	}
	// Keep diagnostics in List's filename order. Pending rows are ordered by
	// ID, which can sort differently once the .json suffix is removed.
	slices.SortFunc(pending, func(a, b app.OperationSummary) int { return strings.Compare(a.ID, b.ID) })
	return pending, errors.Join(failures...)
}

// Hashing and validation must observe the contents of the same open file
// even if another writer atomically replaces it during a scan.
func (s *Store) readDocument(id string, dst *bytes.Buffer) error {
	f, err := s.open(id)
	if err != nil {
		return err
	}
	defer f.Close()
	dst.Reset()
	if _, err := dst.ReadFrom(io.LimitReader(f, maxDocument+1)); err != nil {
		return err
	}
	if dst.Len() > maxDocument {
		return domain.E("invalid", "operation exceeds 2 MiB")
	}
	return nil
}
