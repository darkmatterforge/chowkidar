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

# Format package diff into "old → new" pairs.
# Pair the Nth < line with the Nth > line (paste by index) to handle
# consecutive diff blocks where multiple < lines appear before any > line.
PKG_LINES=""
PKG_TABLE_ROWS=""
if [[ -n "$PKG_DIFF" ]]; then
  while IFS='|' read -r old new; do
    PKG_LINES+="  - ${old} → ${new}"$'\n'
    PKG_TABLE_ROWS+="| ${old} | ${new} |"$'\n'
  done < <(paste -d'|' \
    <(printf '%s\n' "$PKG_DIFF" | grep "^< " | sed 's/^< //') \
    <(printf '%s\n' "$PKG_DIFF" | grep "^> " | sed 's/^> //'))
  PKG_LINES="${PKG_LINES%$'\n'}"
fi

# Build the complete security block in a temp file to avoid awk escaping issues.
BLOCK=$(mktemp)
{
  printf '## [%s] - %s\n\n' "$NEW_VER" "$DATE"
  printf '%s\n\n' '### Security'
  printf '%s\n' "- Automated security rebuild"
  if [[ -n "$CVE_LIST" ]]; then
    printf '%s\n' "  - **Fixed CVEs:** ${CVE_LIST}"
  else
    printf '%s\n' "  - **Fixed CVEs:** none"
  fi
  if [[ -n "$PKG_LINES" ]]; then
    printf '%s\n' "  - **OS packages updated:**"
    printf '%s\n' "$PKG_LINES"
  fi
  printf '%s\n' "  - **Trigger:** ${REASON}"
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
BODY_FILE=$(mktemp)
{
  printf '### Automated security patch\n\n'

  printf '#### CVE fixes\n\n'
  if [[ -n "$CVE_LIST" ]]; then
    for cve in $(printf '%s' "$CVE_LIST" | tr ',' ' '); do
      printf '- %s\n' "$cve"
    done
  else
    printf '_No CVEs eliminated in this rebuild._\n'
  fi
  printf '\n'

  printf '#### OS package updates\n\n'
  if [[ -n "$PKG_TABLE_ROWS" ]]; then
    printf '| Published | Candidate |\n'
    printf '| --- | --- |\n'
    printf '%s' "$PKG_TABLE_ROWS"
  else
    printf '_No package version changes._\n'
  fi
  printf '\n'

  printf '#### Trigger\n\n%s\n' "$REASON"
} > "$BODY_FILE"
BODY="$(cat "$BODY_FILE")"
rm -f "$BODY_FILE"

PR_URL=$(gh pr create \
  --title "chore(security): bump to ${VERSION} — ${REASON}" \
  --body "$BODY" \
  --base main \
  --head "$BRANCH")

# Only the PR URL goes to stdout — captured by the calling workflow step.
echo "$PR_URL"
