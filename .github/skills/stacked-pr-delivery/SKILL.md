---
name: stacked-pr-delivery
description: Split an oversized Proficiency change into dependent, reviewable PRs and squash-merge them safely. Use when a PR mixes architecture, behavior, packaging, CI, and documentation.
license: Apache-2.0
compatibility: Requires bash, git, gh, and jq.
---

# Stacked PR delivery

Use this skill when one PR is too large to review as a coherent change.

## Design the stack

1. Inventory the diff by file and responsibility.
2. Choose dependency-safe slices that compile independently. Prefer this order:
   - toolchain and security prerequisites
   - public API and data contract
   - behavior built on that contract
   - packaging and distribution
   - integration tests, CI, and final documentation
3. Keep the original PR open until every replacement PR exists and the top
   branch reproduces its intended tree.
4. Build each branch from the previous stack branch, not from unrelated
   commits.

## Validate each PR

1. Run the smallest complete quality gate for the slice.
2. Open the PR against its immediate parent branch.
3. Request Copilot review.
4. Triage inline comments and suppressed findings.
5. Fix findings in the PR that owns the affected contract.
6. Cascade the updated parent branch into every dependent branch and rerun
   validation.

## Verify stack equivalence

Before merging, compare the top stack branch with the intended final branch:

```bash
git diff --exit-code <expected-final-ref> <top-stack-ref>
```

If review fixes intentionally changed the tree, document those changes and
compare again against the revised expected ref.

Also compare the stack base with current `main` so recent unrelated changes are
not lost:

```bash
git diff --name-status <stack-base>..origin/main
```

Pay special attention to `.gitignore`, workflows, dependency files, and other
frequently edited repository-wide files.

## Squash merge bottom-up

1. Do not delete parent branches while dependent PRs still use them as bases.
2. Squash-merge the first PR.
3. Retarget the next PR to `main`.
4. Merge current `main` into its branch. Squash merging changes commit
   ancestry, so conflicts are expected even when file content is equivalent.
5. Resolve conflicts by content, never by blindly choosing `ours` or `theirs`.
   Preserve concurrent changes from `main`.
6. Run the real main-branch checks, then squash-merge.
7. Repeat until the top PR is merged.
8. Compare final `main` with the validated top tree and inspect `git status`
   for newly unignored generated files.
9. Close the original oversized PR as superseded and link the replacement
   stack.

## Invariants learned from v0.2

- Splitting after implementation is expensive when WIP commits mix concerns;
  establish stack boundaries before broad edits when possible.
- Dependent PR checks do not necessarily run until the PR is retargeted to
  `main`; local validation is necessary but not sufficient.
- Squash merges make equivalent histories conflict. Resolve the content and
  rerun checks rather than rewriting stack history.
- A conflict resolution dropped `.cache/` from `.gitignore`; always diff
  repository-wide files against current `main` after the final merge.
- E2E cleanup must track the actual server PID, not a shell subshell.
- Performance tests need isolated workloads and explicit noise tolerances.
