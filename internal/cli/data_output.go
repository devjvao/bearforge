package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/colorprofile"

	"bearforge/internal/app"
	"bearforge/internal/domain"
)

func renderData(data any, p *colorprofile.Writer, widths ...int) error {
	if data == nil {
		return nil
	}
	value := reflect.ValueOf(data)
	if (value.Kind() == reflect.Slice || value.Kind() == reflect.Array) && value.Len() == 0 {
		_, err := fmt.Fprintln(p, "No entries.")
		return err
	}
	var headers []string
	var rows [][]string
	switch data := data.(type) {
	case []domain.Worktree:
		headers = []string{"PATH", "BRANCH", "HEAD", "STATE"}
		for _, worktree := range data {
			var flags []string
			if worktree.Bare {
				flags = append(flags, "bare")
			}
			if worktree.Locked {
				flags = append(flags, "locked")
			}
			if worktree.Prunable {
				flags = append(flags, "prunable")
			}
			if worktree.Branch == "" && !worktree.Bare {
				flags = append(flags, "detached")
			}
			rows = append(rows, []string{worktree.Path, worktree.Branch, worktree.Head, strings.Join(flags, ", ")})
		}
	case []app.Operation:
		headers = []string{"ID", "COMMAND", "WORKSPACE", "STATE", "CREATED"}
		for _, operation := range data {
			rows = append(rows, []string{operation.ID, operation.Kind, operation.Workspace, operation.State, operation.Created.Format(time.RFC3339Nano)})
		}
	default:
		encoded, err := json.Marshal(data)
		if err != nil {
			return fmt.Errorf("encode human data: %w", err)
		}
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.UseNumber()
		var decoded any
		if err := decoder.Decode(&decoded); err != nil {
			return fmt.Errorf("decode human data: %w", err)
		}
		if decoded == nil {
			return nil
		}
		if text, ok := decoded.(string); ok {
			_, err := fmt.Fprintln(p, repositoryListText(text))
			return err
		}
		headers, rows = dataRecordTable(decoded)
		if headers == nil {
			headers = []string{"FIELD", "VALUE"}
			flattenData("", decoded, &rows)
		}
	}
	for _, row := range rows {
		for index := range row {
			row[index] = repositoryListText(row[index])
		}
	}
	width := math.MaxInt
	if len(widths) > 0 {
		width = widths[0]
	}
	_, err := fmt.Fprintln(p, renderResultRows(p, headers, rows, width))
	return err
}

func dataRecordTable(value any) ([]string, [][]string) {
	entries, ok := value.([]any)
	if !ok || len(entries) == 0 {
		return nil, nil
	}
	keys := make([]string, 0)
	seen := make(map[string]bool)
	for _, entry := range entries {
		record, ok := entry.(map[string]any)
		if !ok || len(record) == 0 {
			return nil, nil
		}
		for key, cell := range record {
			switch cell.(type) {
			case map[string]any, []any:
				return nil, nil
			}
			if !seen[key] {
				keys = append(keys, key)
				seen[key] = true
			}
		}
	}
	slices.Sort(keys)
	headers := make([]string, len(keys))
	for index, key := range keys {
		headers[index] = repositoryListText(strings.ToUpper(key))
	}
	rows := make([][]string, 0, len(entries))
	for _, entry := range entries {
		record := entry.(map[string]any)
		row := make([]string, len(keys))
		for index, key := range keys {
			if cell, exists := record[key]; exists {
				row[index] = "null"
				if cell != nil {
					row[index] = fmt.Sprint(cell)
				}
			}
		}
		rows = append(rows, row)
	}
	return headers, rows
}

func flattenData(path string, value any, rows *[][]string) {
	switch value := value.(type) {
	case map[string]any:
		if len(value) == 0 {
			*rows = append(*rows, []string{path, "(empty object)"})
			return
		}
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			child := key
			if path != "" {
				child = path + "." + key
			}
			flattenData(child, value[key], rows)
		}
	case []any:
		if len(value) == 0 {
			*rows = append(*rows, []string{path, "No entries."})
			return
		}
		for index, item := range value {
			flattenData(path+"["+strconv.Itoa(index)+"]", item, rows)
		}
	default:
		text := "null"
		if value != nil {
			text = fmt.Sprint(value)
		}
		if path == "" {
			path = "value"
		}
		*rows = append(*rows, []string{path, text})
	}
}
