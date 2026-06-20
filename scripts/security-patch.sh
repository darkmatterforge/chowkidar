#!/usr/bin/env bash
# Usage: scripts/security-patch.sh <version> <reason> [pkg-diff]
# Creates a release branch, updates CHANGELOG, opens a PR with auto-merge enabled.
# Called by the security-patch GitHub Action; requires gh CLI authenticated.
set -euo pipefail

VERSION="$1"          # e.g. v0.3.1
REASON="$2"           # e.g. "wolfi-base updated; fixed 2 CVE(s)"
PKG_DIFF="${3:-}"     # optional package diff summary (raw diff lines)
CVE_LIST="${4:-}"     # optional comma-separated fixed CVE IDs

CHANGELOG="CHANGELOG.md"
DATE=$(date -u +%Y-%m-%d)
BRANCH="release/${VERSION}"
REPO_URL="$(git remote get-url origin | sed 's/\.git$//' | sed 's|git@github.com:|https://github.com/|')"
LATEST_TAG=$(git tag --sort=-version:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -1)

# ── Update CHANGELOG ──────────────────────────────────────────────────────────
TEMP=$(mktemp)
LATEST_VER="${LATEST_TAG#v}"
NEW_VER="${VERSION#v}"
NEW_LINK="[${NEW_VER}]: ${REPO_URL}/compare/${LATEST_TAG}...${VERSION}"

# Format package diff into "old-pkg → new-pkg" pairs for readability.
PKG_LINES=""
if [[ -n "$PKG_DIFF" ]]; then
  PKG_LINES=$(echo "$PKG_DIFF" | awk '
    /^< / { sub(/^< /,""); old=$0; next }
    /^> / { sub(/^> /,""); if (old != "") print "  - " old " → " $0; old="" }
  ')
fi

# Build the complete security block in a temp file to avoid awk escaping issues.
BLOCK=$(mktemp)
{
  printf '## [%s] - %s\n\n' "$NEW_VER" "$DATE"
  printf '### Security\n\n'
  printf '- Automated rebuild: %s\n' "$REASON"
  if [[ -n "$CVE_LIST" ]]; then
    printf '  - **Fixed CVEs:** %s\n' "$CVE_LIST"
  fi
  if [[ -n "$PKG_LINES" ]]; then
    printf '  - **Updated packages:**\n'
    printf '%s\n' "$PKG_LINES"
  fi
  printf '\n'
} > "$BLOCK"

# Insert the block after the ## [Unreleased] section, before the next versioned heading.
awk -v block="$BLOCK" '
  /^## \[Unreleased\]/ { past_unreleased = 1 }
  past_unreleased && !inserted && /^## \[/ && !/^## \[Unreleased\]/ {
    while ((getline line < block) > 0) print line
    inserted = 1
  }
  { print }
' "$CHANGELOG" > "$TEMP" && mv "$TEMP" "$CHANGELOG"
rm -f "$BLOCK"

# Update or insert comparison links at bottom, matching existing link style.
TEMP=$(mktemp)
if grep -q "^\[Unreleased\]:" "$CHANGELOG"; then
  awk -v unreleased="[Unreleased]: ${REPO_URL}/compare/${VERSION}...HEAD" \
      -v new_link="$NEW_LINK" '
    /^\[Unreleased\]:/ { print unreleased; print new_link; next }
    { print }
  ' "$CHANGELOG" > "$TEMP" && mv "$TEMP" "$CHANGELOG"
elif grep -q "^\[${LATEST_VER}\]:" "$CHANGELOG"; then
  awk -v new_link="$NEW_LINK" -v prev="[${LATEST_VER}]:" '
    !inserted && index($0, prev) == 1 { print new_link; inserted = 1 }
    { print }
  ' "$CHANGELOG" > "$TEMP" && mv "$TEMP" "$CHANGELOG"
else
  printf '\n[Unreleased]: %s/compare/%s...HEAD\n%s\n' \
    "$REPO_URL" "$VERSION" "$NEW_LINK" >> "$CHANGELOG"
fi

# ── Bail out if the PR already exists (action re-run after success) ───────────
if EXISTING_PR=$(gh pr list --head "$BRANCH" --json url -q '.[0].url' 2>/dev/null) && [[ -n "$EXISTING_PR" ]]; then
  echo "PR for $BRANCH already exists: $EXISTING_PR — nothing to do." >&2
  echo "$EXISTING_PR"
  exit 0
fi

# ── Commit and push branch ────────────────────────────────────────────────────
# Delete a stale remote branch left by a previous failed run before pushing;
# a clean delete+push avoids force-pushing onto an existing ref.
git push origin --delete "$BRANCH" >/dev/null 2>&1 || true

# Run all git ops in a subshell with stdout redirected to stderr so the only
# thing that ever reaches our stdout is the PR URL printed at the end.
(
  git checkout -b "$BRANCH"
  git config user.name "github-actions[bot]"
  git config user.email "github-actions[bot]@users.noreply.github.com"
  git add "$CHANGELOG"
  git commit -m "chore(security): bump to ${VERSION} — ${REASON}"
  git push origin "$BRANCH"
) >&2

# ── Open PR and enable auto-merge ─────────────────────────────────────────────
BODY="$(printf '### Automated security patch\n\n**Reason:** %s\n\n<details><summary>Package diff</summary>\n\n```\n%s\n```\n\n</details>' "$REASON" "$PKG_DIFF")"

PR_URL=$(gh pr create \
  --title "chore(security): bump to ${VERSION} — ${REASON}" \
  --body "$BODY" \
  --base main \
  --head "$BRANCH")

# Only the PR URL goes to stdout — captured by the calling workflow step.
echo "$PR_URL"
