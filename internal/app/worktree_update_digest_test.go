package app

import "testing"

func TestUpdateDigestDistinguishesNonUTF8Refs(t *testing.T) {
	base := worktreeUpdate{Branch: "topic", Base: updateTarget{Name: "release\xff"}}
	other := base
	other.Base.Name = "release\xfe"
	if base.digest() == other.digest() {
		t.Fatal("two distinct default branch names have the same reviewed digest")
	}
}
