package gitcli

import "testing"

func TestCompareDetachedSHA256HeadWithExactRefAndOID(t *testing.T) {
	c, bare, tree := privateHistoryFixture(t, "files", "sha256")
	ctx := t.Context()
	ref := "refs/remotes/origin/main"
	base, err := c.Resolve(ctx, tree, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, c, bare, "update-ref", ref, base)
	runGit(t, c, tree, "checkout", "--detach")
	runGit(t, c, tree, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "detached work")
	st, err := c.Inspect(ctx, tree)
	if err != nil || !st.Detached || len(st.Head) != 64 {
		t.Fatalf("SHA-256 detached checkout = %+v, %v", st, err)
	}
	for _, configured := range []string{ref, base} {
		d, err := c.CompareBase(ctx, bare, st.Head, configured)
		if err != nil || d.Ahead != 1 || d.Behind != 0 {
			t.Errorf("comparison with %q = %+v, %v", configured, d, err)
		}
	}
}

func TestCompareBaseIgnoresReplacementAncestry(t *testing.T) {
	c, bare, tree := privateHistoryFixture(t, "files", "sha1")
	ctx := t.Context()
	base, err := c.Resolve(ctx, tree, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, c, tree, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "child")
	head, err := c.Resolve(ctx, tree, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	// Grafting the child as a root makes the two commits appear unrelated.
	runGit(t, c, bare, "replace", "--graft", head)
	d, err := c.CompareBase(ctx, bare, head, base)
	if err != nil || d.Ahead != 1 || d.Behind != 0 {
		t.Fatalf("comparison with replaced ancestry = %+v, %v; want 1 ahead only", d, err)
	}
}
