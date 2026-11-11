package operation

import (
	"context"
	"testing"
	"time"

	"bearforge/internal/config"
)

func TestRootLockExcludesOtherWriters(t *testing.T) {
	files := config.New(t.TempDir())
	first, second := New(files), New(files)
	release, err := first.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if unlock, err := second.Lock(ctx); err == nil {
		unlock()
		release()
		t.Fatal("concurrent writer acquired lock")
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	unlock, err := second.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
}
