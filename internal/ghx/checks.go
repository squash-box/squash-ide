package ghx

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	runexec "github.com/squashbox/squash-ide/internal/exec"
)

// ChecksState is the rolled-up CI status of a branch's PR, as surfaced by the
// squash-ide progress strip (T-053).
type ChecksState string

const (
	// ChecksPassing — every check completed successfully (or there are no
	// checks but a PR exists and nothing is failing).
	ChecksPassing ChecksState = "passing"
	// ChecksFailing — at least one check failed/errored/was cancelled.
	ChecksFailing ChecksState = "failing"
	// ChecksPending — a PR exists and at least one check is still running (and
	// none have failed yet); also the empty-rollup case (no checks reported).
	ChecksPending ChecksState = "pending"
	// ChecksNone — no PR exists, or gh is unavailable. Renders as a grey light.
	ChecksNone ChecksState = "none"
)

// PRChecksForBranch returns the rolled-up CI state for the PR whose head is
// branch in the repo at repoPath. It shares PRURLForBranch's optional-capability
// discipline: ErrGHMissing / ErrNoPR / ErrNonGitHubRemote are degraded states
// the caller renders as a grey light, never an error badge.
//
// Errors:
//   - ErrGHMissing      — gh is not on PATH; the runner is not invoked.
//   - ErrNoPR           — gh ran but found no PR for the head; state ChecksNone.
//   - ErrNonGitHubRemote — origin is not a GitHub URL; state ChecksNone.
//   - other errors      — wrapped with %w; surface them to the caller.
func PRChecksForBranch(repoPath, branch string) (ChecksState, error) {
	return PRChecksForBranchWith(runner, repoPath, branch)
}

// PRChecksForBranchWith is PRChecksForBranch with an explicit Runner, for tests.
func PRChecksForBranchWith(r runexec.Runner, repoPath, branch string) (ChecksState, error) {
	if _, err := r.LookPath("gh"); err != nil {
		return ChecksNone, fmt.Errorf("%w: %v", ErrGHMissing, err)
	}

	ctx := context.Background()
	out, err := r.Output(ctx, "git", "-C", repoPath, "remote", "get-url", "origin")
	if err != nil {
		return ChecksNone, fmt.Errorf("git remote get-url origin: %w", err)
	}
	slug, err := parseGitHubSlug(string(out))
	if err != nil {
		return ChecksNone, err
	}

	out, err = r.Output(ctx, "gh", "pr", "view", branch,
		"-R", slug,
		"--json", "statusCheckRollup",
	)
	if err != nil {
		if isNoPRErr(err) {
			return ChecksNone, fmt.Errorf("%w: %s", ErrNoPR, branch)
		}
		return ChecksNone, fmt.Errorf("gh pr view: %w", err)
	}
	return rollupChecks(out)
}

// isNoPRErr reports whether a gh error is the "no PR for this branch" case.
// gh exits non-zero with this message on `pr view` when the head has no PR;
// we map it to ErrNoPR so the caller degrades to a grey light rather than
// treating it as a hard failure.
func isNoPRErr(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "no pull requests found")
}

// rollupChecks reduces a `gh pr view --json statusCheckRollup` payload to a
// single ChecksState. The rule is fail-fast then pending-wins:
//
//	any failed/errored/cancelled check        ⇒ failing
//	else any still-running / pending check    ⇒ pending
//	else (all complete & successful)          ⇒ passing
//	an empty rollup (PR exists, no checks)     ⇒ pending (nothing to call green)
//
// It handles both rollup element shapes gh emits: CheckRun (GitHub Actions —
// status + conclusion) and StatusContext (legacy commit statuses — state).
func rollupChecks(jsonOut []byte) (ChecksState, error) {
	var payload struct {
		StatusCheckRollup []struct {
			Typename   string `json:"__typename"`
			Status     string `json:"status"`     // CheckRun: QUEUED|IN_PROGRESS|COMPLETED
			Conclusion string `json:"conclusion"` // CheckRun: SUCCESS|FAILURE|...
			State      string `json:"state"`      // StatusContext: SUCCESS|PENDING|FAILURE|ERROR
		} `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal(jsonOut, &payload); err != nil {
		return ChecksNone, fmt.Errorf("parsing statusCheckRollup: %w", err)
	}

	checks := payload.StatusCheckRollup
	if len(checks) == 0 {
		return ChecksPending, nil // PR exists but no checks reported (yet)
	}

	pending := false
	for _, c := range checks {
		if c.Typename == "CheckRun" {
			if strings.ToUpper(c.Status) != "COMPLETED" {
				pending = true
				continue
			}
			switch strings.ToUpper(c.Conclusion) {
			case "SUCCESS", "NEUTRAL", "SKIPPED", "":
				// not-failing. An empty conclusion on a COMPLETED run (gh
				// occasionally reports this, or a future conclusion value) is
				// treated as benign rather than red — a false-failing CI light is
				// worse than an optimistic one.
			default: // FAILURE, TIMED_OUT, CANCELLED, ACTION_REQUIRED, STARTUP_FAILURE
				return ChecksFailing, nil
			}
			continue
		}
		// StatusContext (legacy) and any unknown shape with a "state" field.
		switch strings.ToUpper(c.State) {
		case "SUCCESS", "":
			// ok / no info
		case "PENDING", "EXPECTED":
			pending = true
		default: // FAILURE, ERROR
			return ChecksFailing, nil
		}
	}

	if pending {
		return ChecksPending, nil
	}
	return ChecksPassing, nil
}
