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

# Update comparison links at the bottom
if grep -q "\[Unreleased\]:" "$TEMP_FILE"; then
  sed -i.bak \
    -e "s|\[Unreleased\]: .*|\[Unreleased\]: ${REPO_URL}/compare/${NEW_TAG}...HEAD|" \
    "$TEMP_FILE"
  # Add new version link before the first versioned link
  PREV_TAG="${LATEST_TAG:-}"
  if [[ -n "$PREV_TAG" ]]; then
    NEW_LINK="[${NEW_VERSION}]: ${REPO_URL}/compare/${PREV_TAG}...${NEW_TAG}"
  else
    NEW_LINK="[${NEW_VERSION}]: ${REPO_URL}/releases/tag/${NEW_TAG}"
  fi
  sed -i.bak "/^\[Unreleased\]:/a\\
${NEW_LINK}" "$TEMP_FILE"
  rm -f "${TEMP_FILE}.bak"
else
  # Append links section
  {
    echo ""
    echo "[Unreleased]: ${REPO_URL}/compare/${NEW_TAG}...HEAD"
    if [[ -n "${LATEST_TAG:-}" ]]; then
      echo "[${NEW_VERSION}]: ${REPO_URL}/compare/${LATEST_TAG}...${NEW_TAG}"
    else
      echo "[${NEW_VERSION}]: ${REPO_URL}/releases/tag/${NEW_TAG}"
    fi
  } >> "$TEMP_FILE"
fi

mv "$TEMP_FILE" "$CHANGELOG"

# ── Commit, tag, push ────────────────────────────────────────────────────────
git add "$CHANGELOG"
git commit -m "chore: release ${NEW_TAG}"
git tag -a "$NEW_TAG" -m "Release ${NEW_TAG}"
git push origin main
git push origin "$NEW_TAG"

echo ""
echo "✓ Released ${NEW_TAG}"
echo "  GitHub Release + Docker publish workflows will trigger automatically."
