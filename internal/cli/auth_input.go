package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/devjvao/bearforge/internal/domain"
)

// readAuthInput joins the reader before returning cancellation. It never leaves
// a goroutine consuming input or changing terminal state after a prompt exits.
func readAuthInput(ctx context.Context, input io.Reader, read func(io.Reader) (string, error)) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if file, ok := input.(*os.File); ok {
		return readAuthFile(ctx, file, read)
	}
	if closer, ok := input.(io.ReadCloser); ok {
		return readAuthCancelable(ctx, closer, read, func() { _ = closer.Close() })
	}
	// These readers cannot block. Arbitrary readers must expose Close so a
	// blocked read can be interrupted without abandoning a background goroutine.
	switch input.(type) {
	case *strings.Reader, *bytes.Reader, *bytes.Buffer:
		return read(input)
	default:
		return "", domain.E("invalid", "authentication input must support cancellation")
	}
}

func readAuthCancelable(ctx context.Context, input io.Reader, read func(io.Reader) (string, error), cancel func()) (string, error) {
	var value string
	var err error
	done := make(chan struct{})
	go func() {
		defer close(done)
		value, err = read(input)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		cancel()
		<-done
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return value, err
}

func readAuthHidden(ctx context.Context, input io.Reader, prepare func() (func() error, error)) (value string, err error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	restore, err := prepare()
	if err != nil {
		return "", err
	}
	defer func() {
		err = errors.Join(err, restore())
		if err != nil {
			value = ""
		}
	}()
	return readAuthInput(ctx, input, readAuthSecretLine)
}

// Raw mode avoids echo and lets Ctrl-C cancel even while signal handling is
// suspended by the terminal. Read one byte at a time to preserve the next prompt.
func readAuthSecretLine(input io.Reader) (string, error) {
	var value []byte
	var buffer [1]byte
	for len(value) <= 64<<10 {
		if _, err := io.ReadFull(input, buffer[:]); err != nil {
			return "", err
		}
		switch buffer[0] {
		case '\r', '\n':
			return string(value), nil
		case 3:
			return "", context.Canceled
		case 4:
			return "", io.EOF
		case 8, 127:
			if len(value) > 0 {
				_, size := utf8.DecodeLastRune(value)
				value = value[:len(value)-size]
			}
		default:
			if buffer[0] >= 32 {
				value = append(value, buffer[0])
			}
		}
	}
	return "", domain.E("invalid", "authentication input exceeds its limit")
}
