#!/usr/bin/env bash
# Usage: ./scripts/release.sh [patch|minor|major]
# Bumps the version, updates CHANGELOG.md, commits, tags, and pushes.
set -euo pipefail

# Normalize bump argument to lowercase and default to 'patch'
BUMP="${1:-patch}"
# POSIX-compatible lowercase conversion (avoid bash-only ${var,,})
BUMP="$(printf '%s' "$BUMP" | tr '[:upper:]' '[:lower:]')"
CHANGELOG="CHANGELOG.md"

# ── Validate working tree ────────────────────────────────────────────────────
if [[ -n "$(git status --porcelain -- "$CHANGELOG")" ]]; then
  echo "Error: $CHANGELOG has uncommitted changes. Commit or stash first." >&2
  exit 1
fi
if [[ -n "$(git status --porcelain)" ]]; then
  echo "Warning: working tree is dirty. Only $CHANGELOG is managed by this script."
fi

# ── Determine current version ────────────────────────────────────────────────
LATEST_TAG="$(git tag --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -1 || true)"
if [[ -z "$LATEST_TAG" ]]; then
  CURRENT="0.0.0"
else
  CURRENT="${LATEST_TAG#v}"
fi

IFS='.' read -r MAJOR MINOR PATCH <<< "$CURRENT"

case "$BUMP" in
  major|maj|majot)
    MAJOR=$((MAJOR + 1))
    MINOR=0
    PATCH=0
    ;;
  minor|min|minot)
    MINOR=$((MINOR + 1))
    PATCH=0
    ;;
  patch|p)
    PATCH=$((PATCH + 1))
    ;;
  *)
    echo "Usage: $0 [patch|minor|major] (aliases: p, min, maj; accepts common misspellings)" >&2
    exit 1
    ;;
esac

NEW_VERSION="${MAJOR}.${MINOR}.${PATCH}"
NEW_TAG="v${NEW_VERSION}"
TODAY="$(date +%Y-%m-%d)"
REPO_URL="$(git remote get-url origin | sed 's/\.git$//' | sed 's|git@github.com:|https://github.com/|')"

echo "Bumping: ${CURRENT} → ${NEW_VERSION} (${BUMP})"

# ── Check [Unreleased] has content ───────────────────────────────────────────
UNRELEASED_CONTENT=$(awk '/^## \[Unreleased\]/{found=1; next} found && /^## \[/{exit} found && /\S/{print}' "$CHANGELOG")
if [[ -z "$UNRELEASED_CONTENT" ]]; then
  echo "Error: [Unreleased] section in $CHANGELOG is empty. Add your changes first." >&2
  exit 1
fi

# ── Update CHANGELOG.md ──────────────────────────────────────────────────────
# Replace "## [Unreleased]" with a new Unreleased block + the versioned entry
TEMP_FILE=$(mktemp)
awk -v ver="$NEW_VERSION" -v date="$TODAY" '
  /^## \[Unreleased\]/ {
    print "## [Unreleased]"
    print ""
    print "## [" ver "] - " date
    next
  }
  { print }
' "$CHANGELOG" > "$TEMP_FILE"

# Update comparison links at the bottom (newest first).
if [[ -n "$LATEST_TAG" ]]; then
  NEW_LINK="[${NEW_VERSION}]: ${REPO_URL}/compare/${LATEST_TAG}...${NEW_TAG}"
else
  NEW_LINK="[${NEW_VERSION}]: ${REPO_URL}/releases/tag/${NEW_TAG}"
fi
UNRELEASED_LINK="[Unreleased]: ${REPO_URL}/compare/${NEW_TAG}...HEAD"

if grep -q "^\[Unreleased\]:" "$TEMP_FILE"; then
  # [Unreleased]: already exists — update it and insert new version link after it
  awk -v unreleased="$UNRELEASED_LINK" -v new_link="$NEW_LINK" '
    /^\[Unreleased\]:/ { print unreleased; print new_link; next }
    { print }
  ' "$TEMP_FILE" > "${TEMP_FILE}.new" && mv "${TEMP_FILE}.new" "$TEMP_FILE"
elif grep -qE "^\[[0-9]" "$TEMP_FILE"; then
  # Version links exist but no [Unreleased]: — insert both before the first version link
  awk -v unreleased="$UNRELEASED_LINK" -v new_link="$NEW_LINK" '
    !inserted && /^\[[0-9]/ { print unreleased; print new_link; inserted=1 }
    { print }
  ' "$TEMP_FILE" > "${TEMP_FILE}.new" && mv "${TEMP_FILE}.new" "$TEMP_FILE"
else
  # No links section at all — append one
  { echo ""; echo "$UNRELEASED_LINK"; echo "$NEW_LINK"; } >> "$TEMP_FILE"
fi

mv "$TEMP_FILE" "$CHANGELOG"

# ── Commit, open PR, wait for CI on branch, then admin-merge and tag ─────────
# CI runs on the branch before anything touches main.
# If CI fails the PR is closed and main stays clean — no orphaned changelog entry.
RELEASE_BRANCH="release/${NEW_TAG}"
git checkout -b "$RELEASE_BRANCH"
git add "$CHANGELOG"
git commit -m "chore: release ${NEW_TAG}"
git push origin "$RELEASE_BRANCH"

BRANCH_SHA="$(git rev-parse HEAD)"

echo ""
echo "Opening PR for ${NEW_TAG}..."
PR_URL=$(gh pr create \
  --title "chore: release ${NEW_TAG}" \
  --body "Automated changelog commit for ${NEW_TAG}." \
  --base main \
  --head "$RELEASE_BRANCH")
echo "  PR: ${PR_URL}"

echo ""
echo "Waiting for CI on branch (this can take 10-15 min)..."
RUN_ID=""
for i in $(seq 1 60); do
  RUN_ID=$(gh run list --commit "$BRANCH_SHA" --workflow ci.yml \
    --json databaseId --jq '.[0].databaseId // empty' 2>/dev/null || true)
  [[ -n "$RUN_ID" ]] && break
  echo "  Run not registered yet (${i}/60)..."
  sleep 10
done

if [[ -z "$RUN_ID" ]]; then
  echo "Error: CI run never appeared. Closing PR — main is untouched." >&2
  gh pr close "$PR_URL" --delete-branch
  exit 1
fi

echo "  CI run ID: ${RUN_ID}"
if ! gh run watch "$RUN_ID" --exit-status; then
  echo ""
  echo "CI failed — closing PR. main is untouched, no changelog committed." >&2
  gh pr close "$PR_URL" --delete-branch
  exit 1
fi
echo "  CI passed ✓"

echo "Merging with admin override (bypasses review requirement)..."
gh pr merge "$PR_URL" --admin --merge --delete-branch

git checkout main
git pull origin main
git tag -a "$NEW_TAG" -m "Release ${NEW_TAG}"
git push origin "$NEW_TAG"

echo ""
echo "✓ Released ${NEW_TAG}"
echo "  GitHub Release + Docker publish workflows will trigger automatically."
