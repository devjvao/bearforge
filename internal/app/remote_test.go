package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeRemotePreservesTransportURLs(t *testing.T) {
	for _, remote := range []string{
		"https://example.com/team/repo.git",
		"ssh://git@example.com:2222/team/repo.git",
		"git@example.com:team/repo.git",
		"example.com:team/repo.git",
		"file:///tmp/repo.git",
	} {
		t.Run(remote, func(t *testing.T) {
			got, err := normalizeRemote(remote)
			if err != nil || got != remote {
				t.Fatalf("transport remote changed: got %q, want %q, error %v", got, remote, err)
			}
		})
	}
}

func TestNormalizeRemoteLocalPaths(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(cwd, "remote")
	for _, remote := range []string{"./remote", want} {
		got, err := normalizeRemote(remote)
		if err != nil || got != want {
			t.Fatalf("got %q, want stable path %q, error %v", got, want, err)
		}
	}
}
