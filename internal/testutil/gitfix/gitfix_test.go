package gitfix

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNewBareOrigin_HasMainBranch(t *testing.T) {
	origin := NewBareOrigin(t)

	if _, err := os.Stat(filepath.Join(origin, "HEAD")); err != nil {
		t.Fatalf("bare repo missing HEAD: %v", err)
	}

	out, err := exec.Command("git", "-C", origin, "rev-parse", "refs/heads/main").CombinedOutput()
	if err != nil {
		t.Fatalf("rev-parse main: %v\n%s", err, out)
	}
}

func TestClone_ProducesWorkingCopy(t *testing.T) {
	origin := NewBareOrigin(t)
	wc := Clone(t, origin)

	if _, err := os.Stat(filepath.Join(wc, "README.md")); err != nil {
		t.Fatalf("clone missing README.md: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wc, ".git")); err != nil {
		t.Fatalf("clone missing .git: %v", err)
	}
}
