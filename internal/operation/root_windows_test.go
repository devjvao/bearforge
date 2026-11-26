package operation

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestJournalRootWithWindowsShortNames(t *testing.T) {
	base, _ := operationFixture(t)
	current := filepath.Join(base.Root, "Legacy directory with a long name", "Managed directory with a long name")
	if err := os.MkdirAll(current, 0700); err != nil {
		t.Fatal(err)
	}
	path, err := windows.UTF16PtrFromString(current)
	if err != nil {
		t.Fatal(err)
	}
	size, err := windows.GetShortPathName(path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, size)
	if _, err := windows.GetShortPathName(path, &buf[0], size); err != nil {
		t.Fatal(err)
	}
	short := windows.UTF16ToString(buf)
	if strings.EqualFold(filepath.Base(short), filepath.Base(current)) ||
		strings.EqualFold(filepath.Base(filepath.Dir(short)), filepath.Base(filepath.Dir(current))) {
		t.Skip("volume does not assign DOS short names to the fixture directories")
	}
	for _, recorded := range []string{
		short,
		filepath.Join(filepath.Dir(short), filepath.Base(current)),
		filepath.Join(filepath.Dir(current), filepath.Base(short)),
	} {
		store := journalWithRecordedRoot(t, current, recorded)
		op, err := store.Load("task")
		if err != nil || op.Root != current {
			t.Fatalf("load journal recorded at %q: %+v, %v", recorded, op, err)
		}
	}
}

func TestJournalRootRejectsShortLookingJunction(t *testing.T) {
	base, _ := operationFixture(t)
	parent := filepath.Join(base.Root, "Managed directory with a long name")
	alias := filepath.Join(base.Root, "REDIR~1")
	store := journalWithRecordedRoot(t, filepath.Join(parent, "root"), filepath.Join(alias, "root"))
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"New-Item -ItemType Junction -Path $env:TEST_JUNCTION -Target $env:TEST_TARGET -ErrorAction Stop | Out-Null")
	cmd.Env = append(os.Environ(), "TEST_JUNCTION="+alias, "TEST_TARGET="+parent)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create junction: %v: %s", err, out)
	}
	if _, err := store.Load("task"); err == nil || !strings.Contains(err.Error(), "schema/root mismatch") {
		t.Fatalf("accepted a journal linked through a junction: %v", err)
	}
}
