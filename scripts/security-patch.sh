#!/usr/bin/env bash
# Usage: scripts/security-patch.sh <version> <reason> [pkg-diff]
# Creates a release branch, updates CHANGELOG, opens a PR with auto-merge enabled.
# Called by the security-patch GitHub Action; requires gh CLI authenticated.
set -euo pipefail

VERSION="$1"          # e.g. v0.3.1
REASON="$2"           # e.g. "wolfi-base updated; fixed 2 CVE(s)"
PKG_DIFF="${3:-}"     # optional package diff summary

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

# Insert versioned entry before the first ## [ heading only (awk stops after first match).
# Mirrors the pattern used by release.sh.
awk -v ver="$NEW_VER" -v date="$DATE" -v reason="$REASON" '
  !inserted && /^## \[/ {
    print "## [" ver "] - " date
    print ""
    print "### Security"
    print "- Automated rebuild: " reason
    print ""
    inserted = 1
  }
  { print }
' "$CHANGELOG" > "$TEMP" && mv "$TEMP" "$CHANGELOG"

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

# ── Commit and push branch ────────────────────────────────────────────────────
git checkout -b "$BRANCH"
git config user.name "github-actions[bot]"
git config user.email "github-actions[bot]@users.noreply.github.com"
git add "$CHANGELOG"
git commit -m "chore(security): bump to ${VERSION} — ${REASON}"
git push origin "$BRANCH"

BRANCH_SHA="$(git rev-parse HEAD)"
echo "branch_sha=${BRANCH_SHA}"

# ── Open PR and enable auto-merge ─────────────────────────────────────────────
BODY="$(printf '### Automated security patch\n\n**Reason:** %s\n\n<details><summary>Package diff</summary>\n\n```\n%s\n```\n\n</details>' "$REASON" "$PKG_DIFF")"

PR_URL=$(gh pr create \
  --title "chore(security): bump to ${VERSION} — ${REASON}" \
  --body "$BODY" \
  --base main \
  --head "$BRANCH")

echo "PR: ${PR_URL}"

# Auto-merge: GitHub merges the PR automatically once all required CI checks pass.
gh pr merge "$PR_URL" --auto --merge

echo "Auto-merge enabled — PR will merge once CI passes."
