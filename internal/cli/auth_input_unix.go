//go:build !windows

package cli

import (
	"context"
	"io"
	"os"

	"github.com/muesli/cancelreader"
)

func readAuthFile(ctx context.Context, file *os.File, read func(io.Reader) (string, error)) (string, error) {
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if info.Mode().IsRegular() {
		return read(file)
	}
	input, err := cancelreader.NewReader(file)
	if err != nil {
		return "", err
	}
	defer input.Close()
	return readAuthCancelable(ctx, input, read, func() { input.Cancel() })
}
