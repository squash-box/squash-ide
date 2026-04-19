// Package ghx wraps the `gh` CLI for the read-only PR queries dispatch.Complete
// uses to capture a branch's PR URL at archive time. The gh binary is shelled
// out via the internal/exec.Runner seam so unit tests can stub it.
//
// gh is treated as an optional capability: callers branch on the typed
// sentinel errors ErrGHMissing and ErrNoPR via errors.Is and degrade to a
// missing-URL state without aborting the operation that needed the lookup.
package ghx

import (
	"context"
	"errors"
	"fmt"
	"strings"

	runexec "github.com/squashbox/squash-ide/internal/exec"
)

// ErrGHMissing is returned when the gh binary is not on PATH. Callers should
// treat this as "URL unavailable, continue without one" rather than fatal —
// the gh dependency is documented but not strictly required for completion
// to succeed.
var ErrGHMissing = errors.New("gh binary not on PATH")

// ErrNoPR is returned when gh runs successfully but reports no PR for the
// requested branch. Same degradation semantics as ErrGHMissing.
var ErrNoPR = errors.New("no PR found for branch")

// ErrNonGitHubRemote is returned when the repo's `origin` remote is not a
// GitHub URL — gh cannot help in that case. Same degradation semantics as
// ErrGHMissing / ErrNoPR: warn and continue with no PR URL. Surfaces e.g.
// during e2e tests against a local bare-git origin or self-hosted Gitea.
var ErrNonGitHubRemote = errors.New("origin remote is not a GitHub URL")

// runner is the package-level Runner used by PRURLForBranch. Swap in tests
// via SetRunner.
var runner runexec.Runner = runexec.Default

// SetRunner swaps the package-level runner. Returns the previous value so
// callers can restore it in cleanup. Intended for tests only.
func SetRunner(r runexec.Runner) runexec.Runner {
	prev := runner
	runner = r
	return prev
}

// PRURLForBranch returns the URL of the PR whose head matches branch in the
// repo at repoPath. It first derives the GitHub repo slug (owner/name) from
// `git -C <repoPath> remote get-url origin` so the gh call can target the
// repo explicitly without relying on cwd — keeping the call cwd-independent
// matches the discipline established by [[T-026]].
//
// Errors:
//   - ErrGHMissing  — gh is not on PATH; the runner is not invoked.
//   - ErrNoPR       — gh ran but returned no URL (no open PR for the head).
//   - other errors  — wrapped with %w; surface them to the caller.
func PRURLForBranch(repoPath, branch string) (string, error) {
	return PRURLForBranchWith(runner, repoPath, branch)
}

// PRURLForBranchWith is PRURLForBranch with an explicit Runner, for tests.
func PRURLForBranchWith(r runexec.Runner, repoPath, branch string) (string, error) {
	if _, err := r.LookPath("gh"); err != nil {
		return "", fmt.Errorf("%w: %v", ErrGHMissing, err)
	}

	ctx := context.Background()
	out, err := r.Output(ctx, "git", "-C", repoPath, "remote", "get-url", "origin")
	if err != nil {
		return "", fmt.Errorf("git remote get-url origin: %w", err)
	}
	slug, err := parseGitHubSlug(string(out))
	if err != nil {
		return "", err
	}

	out, err = r.Output(ctx, "gh", "pr", "list",
		"-R", slug,
		"--head", branch,
		"--json", "url",
		"--jq", ".[0].url",
	)
	if err != nil {
		return "", fmt.Errorf("gh pr list: %w", err)
	}
	url := strings.TrimSpace(string(out))
	if url == "" {
		return "", fmt.Errorf("%w: %s", ErrNoPR, branch)
	}
	return url, nil
}

// parseGitHubSlug extracts "owner/name" from a GitHub remote URL. Accepts
// the common SSH (`git@github.com:owner/name.git`) and HTTPS
// (`https://github.com/owner/name.git`) forms. Other hosts are rejected so
// we never claim a non-GitHub remote belongs to a `gh`-known repo.
func parseGitHubSlug(remote string) (string, error) {
	s := strings.TrimSpace(remote)
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimSuffix(s, "/")

	if i := strings.Index(s, "@github.com:"); i >= 0 {
		return s[i+len("@github.com:"):], nil
	}
	for _, prefix := range []string{
		"https://github.com/",
		"http://github.com/",
		"git://github.com/",
		"ssh://git@github.com/",
	} {
		if strings.HasPrefix(s, prefix) {
			return strings.TrimPrefix(s, prefix), nil
		}
	}
	return "", fmt.Errorf("%w: %q", ErrNonGitHubRemote, remote)
}
