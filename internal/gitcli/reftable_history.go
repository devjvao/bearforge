package gitcli

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hanwen/reftable"

	"github.com/devjvao/bearforge/internal/domain"
)

// The decoder reads from an immutable, bounded snapshot. Limit individual
// block reads as well as the total input taken from the private stack.
type historyBlocks []byte

func (b historyBlocks) Size() uint64 { return uint64(len(b)) }
func (b historyBlocks) Close() error { return nil }
func (b historyBlocks) ReadBlock(offset uint64, size int) ([]byte, error) {
	if size < 0 || size > 64<<10 || offset > b.Size() || uint64(size) > b.Size()-offset {
		return nil, errors.New("private reftable block exceeds inspection limits")
	}
	return b[offset : offset+uint64(size)], nil
}

// A private stack may contain unpublished tables after an interrupted update.
func checkPrivateStackEntries(root *os.Root, tables []string) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	// One entry beyond the manifest and its listed tables suffices to
	// detect extra files without reading an arbitrarily large directory.
	names, err := dir.Readdirnames(len(tables) + 2)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	for _, name := range names {
		if name != "tables.list" && (strings.HasSuffix(name, ".lock") || !slices.Contains(tables, name)) {
			return privateRefError(root.Name(), name)
		}
	}
	return nil
}

// reftableHistory reads only the selected worktree's private stack. The
// shared repository and other worktrees have independent tables and reflogs.
func reftableHistory(ctx context.Context, admin *os.Root) (tips []string, err error) {
	// The upstream decoder can panic on malformed records or unknown hash
	// IDs. Treat all such input as a refusal to release the registration.
	defer func() {
		if recover() != nil {
			tips, err = nil, domain.E("unsafe_removal", "cannot decode the private reftable in %s; recover it before removal", admin.Name())
		}
	}()
	root, err := admin.OpenRoot("reftable")
	if err != nil {
		return nil, err
	}
	defer root.Close()
	list, err := readPrivateFile(root, "tables.list", 64<<10)
	if err != nil {
		return nil, err
	}
	names := strings.Fields(string(list))
	if len(names) == 0 || len(names) > 128 {
		return nil, domain.E("unsafe_removal", "cannot inspect an empty or oversized private reftable stack in %s", root.Name())
	}
	if err := checkPrivateStackEntries(root, names); err != nil {
		return nil, err
	}
	var tables []reftable.Table
	remaining := maxPrivateBytes
	for _, name := range names {
		if !filepath.IsLocal(name) || filepath.Base(name) != name || strings.ContainsAny(name, ":/\\") {
			return nil, domain.E("unsafe_removal", "invalid private reftable name in %s", root.Name())
		}
		data, err := readPrivateFile(root, name, remaining)
		if err != nil {
			return nil, err
		}
		remaining -= len(data)
		reader, err := reftable.NewReader(historyBlocks(data), name)
		if err != nil {
			return nil, fmt.Errorf("read private reftable %s: %w", name, err)
		}
		tables = append(tables, reader)
	}
	table, err := reftable.NewMerged(tables, tables[0].HashID())
	if err != nil {
		return nil, err
	}
	refs, err := table.SeekRef("")
	if err != nil {
		return nil, err
	}
	for {
		var ref reftable.RefRecord
		ok, err := refs.NextRef(&ref)
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		if ref.IsDeletion() {
			continue
		}
		if ref.RefName != "HEAD" && ref.RefName != "ORIG_HEAD" {
			return nil, privateRefError(admin.Name(), ref.RefName)
		}
		if ref.RefName == "ORIG_HEAD" && ref.Target != "" {
			return nil, privateRefError(admin.Name(), ref.RefName)
		}
		if len(ref.Value) > 0 {
			tips = append(tips, hex.EncodeToString(ref.Value))
		}
	}
	logs, err := table.SeekLog("", math.MaxUint64)
	if err != nil {
		return nil, err
	}
	for decoded := 0; ; {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var record reftable.LogRecord
		ok, err := logs.NextLog(&record)
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		decoded += 64 + len(record.Name) + len(record.Email) + len(record.Message) + len(record.RefName)
		if decoded > maxPrivateBytes {
			return nil, domain.E("unsafe_removal", "private reflogs exceed the inspection limit in %s", root.Name())
		}
		if record.IsDeletion() {
			continue
		}
		if record.RefName != "HEAD" && record.RefName != "ORIG_HEAD" {
			return nil, privateRefError(admin.Name(), record.RefName)
		}
		for _, value := range [][]byte{record.Old, record.New} {
			oid := hex.EncodeToString(value)
			if strings.Trim(oid, "0") != "" {
				tips = append(tips, oid)
			}
		}
	}
	return tips, nil
}
