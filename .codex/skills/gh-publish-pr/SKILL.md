---
name: gh-publish-pr
description: Publish repository changes by reviewing scope, staging, committing, pushing, and opening a GitHub pull request. Use when the user asks to commit, push, publish a branch, create/open a PR, or when GitHub authentication appears broken inside a Codex worktree or macOS sandbox.
---

# Publish GitHub PR

Publish the intended local changes without misdiagnosing sandbox or GitHub connector failures as user authentication failures.

## Authentication

1. Require `gh` and inspect the repository remote.
2. In a Codex worktree on macOS, run `gh auth status` with host escalation on the first attempt. The sandbox may not access Keychain and can falsely report an invalid token.
3. Never ask the user to log in based only on a sandboxed authentication failure.
4. Ask for `gh auth login -h github.com` only when the escalated status check fails.
5. Run authenticated `gh` operations with host escalation so they can access Keychain.

Do not print, copy, or persist authentication tokens.

## Publish workflow

1. Inspect `git status -sb`, the diff, current branch, remotes, and repository default branch.
2. Confirm the intended scope. Never stage unrelated user changes.
3. Stay on an existing feature branch. Create a branch only when currently on the default branch.
4. Stage explicit paths for mixed worktrees; otherwise stage the confirmed complete change.
5. Run `git diff --cached --check` and relevant tests before committing.
6. Commit with a concise message covering the full diff.
7. Push the branch with upstream tracking over SSH.
8. Open a draft PR unless the user explicitly requests ready-for-review.
9. Include summary, rationale, user impact, and validation commands in the PR body.
10. Verify the PR URL, base, head, title, and draft state before reporting completion.

## Sandbox failures

Codex worktree Git metadata may live in a parent repository outside the writable sandbox.

- If `git add` or `git commit` fails on `.git/worktrees/.../index.lock`, retry with host escalation.
- If `git push` fails because network or SSH is sandboxed, retry with host escalation.
- Treat these as environment boundaries, not repository corruption.

## PR creation fallback

Prefer the GitHub connector after pushing. If it returns `Resource not accessible by integration` or HTTP 403, immediately fall back to escalated `gh pr create`; do not ask the user to reauthenticate.

Write multiline PR content to a temporary body file, pass it with `--body-file`, and delete it afterward. Verify the worktree is clean when finished.
