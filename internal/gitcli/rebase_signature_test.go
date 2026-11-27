package gitcli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRebaseProtectsIgnoredPathReplayedBySignedCommits(t *testing.T) {
	sshKeygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skip("SSH commit signing requires ssh-keygen")
	}
	c, bare, path := privateHistoryFixture(t, "files", "sha1")
	ctx := t.Context()
	keyDir := t.TempDir()
	key := filepath.Join(keyDir, "test-key")
	if out, err := exec.CommandContext(ctx, sshKeygen, "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("generate temporary signing key: %v: %s", err, out)
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	signers := filepath.Join(keyDir, "allowed")
	if err := os.WriteFile(signers, append([]byte("test@example.invalid "), pub...), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(t, c, path, "config", "user.name", "Test")
	runGit(t, c, path, "config", "user.email", "test@example.invalid")
	runGit(t, c, path, "config", "gpg.format", "ssh")
	runGit(t, c, path, "config", "user.signingkey", filepath.ToSlash(key))
	runGit(t, c, path, "config", "gpg.ssh.allowedSignersFile", filepath.ToSlash(signers))
	runGit(t, c, path, "config", "log.showSignature", "true")
	runGit(t, c, path, "switch", "-c", "topic")
	ignored := filepath.Join(path, "private.log")
	if err := os.WriteFile(ignored, []byte("tracked temporarily"), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(t, c, path, "add", "--", "private.log")
	runGit(t, c, path, "commit", "-S", "-m", "add file temporarily")
	runGit(t, c, path, "rm", "--", "private.log")
	runGit(t, c, path, "commit", "-S", "-m", "remove file")
	runGit(t, c, path, "verify-commit", "HEAD", "HEAD^")
	base := filepath.Join(t.TempDir(), "base")
	runGit(t, c, bare, "worktree", "add", "--", base, "main")
	runGit(t, c, base, "commit", "--allow-empty", "-m", "new base")
	exclude := filepath.Join(keyDir, "exclude")
	if err := os.WriteFile(exclude, []byte("private.log\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(t, c, path, "config", "core.excludesFile", filepath.ToSlash(exclude))
	if err := os.WriteFile(ignored, []byte("local ignored contents"), 0600); err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "HEAD")))
	target := strings.TrimSpace(string(runGit(t, c, base, "rev-parse", "HEAD")))
	err = c.UpdateCheckedOutBranch(ctx, bare, path, "topic", head, target, true)
	if err == nil || !strings.Contains(err.Error(), "rebasing would overwrite ignored working files at private.log") {
		t.Errorf("rebase did not identify the conflicting ignored path: %v", err)
	}
	if got := strings.TrimSpace(string(runGit(t, c, path, "rev-parse", "HEAD"))); got != head {
		t.Errorf("branch moved during preflight: %s", got)
	}
	if got, err := os.ReadFile(ignored); err != nil || string(got) != "local ignored contents" {
		t.Errorf("ignored file changed: %q, %v", got, err)
	}
}
