#!/usr/bin/env bash
set -e

# Usage:
#   ./push.sh "commit message" patch
#   ./push.sh "commit message" minor
#   ./push.sh "commit message" major

MSG="$1"
BUMP="$2"

if [ -z "$MSG" ] || [ -z "$BUMP" ]; then
    echo "Usage: ./push.sh \"Commit message\" [patch|minor|major]"
    exit 1
fi

IMAGE_BASE="ghcr.io/thefozid/go-llama"

# Get numerically highest tag (strip 'v')
git fetch --tags --quiet
LATEST=$(git tag --list 'v*' | sort -V | tail -n 1)
LATEST=${LATEST#v}
if [ -z "$LATEST" ]; then
  LATEST="0.0.0"
fi


MAJOR=$(echo "$LATEST" | cut -d. -f1)
MINOR=$(echo "$LATEST" | cut -d. -f2)
PATCH=$(echo "$LATEST" | cut -d. -f3)

case "$BUMP" in
  major) MAJOR=$((MAJOR+1)); MINOR=0; PATCH=0 ;;
  minor) MINOR=$((MINOR+1)); PATCH=0 ;;
  patch) PATCH=$((PATCH+1)) ;;
  *) echo "Invalid bump: $BUMP (use patch|minor|major)"; exit 1 ;;
esac

VERSION="$MAJOR.$MINOR.$PATCH"

echo "📌 Previous version: $LATEST"
echo "✨ New version: v$VERSION"
echo

#  --platform linux/amd64,linux/arm64,linux/arm/v7 \


# Build + push images for AMD64 + ARM64
echo "🚀 Building & pushing multi-arch images..."
docker buildx build \
  --platform linux/amd64,linux/arm64,linux/arm/v7 \
  -t "$IMAGE_BASE:latest" \
  -t "$IMAGE_BASE:$VERSION" \
  -t "$IMAGE_BASE:$MAJOR.$MINOR" \
  -t "$IMAGE_BASE:$MAJOR" \
  --push .

echo
echo "📦 Git commit + tag..."
git add .
git commit -m "$MSG" || echo "No changes to commit"
git tag "v$VERSION"
git push
git push --tags

echo
echo "✅ Release v$VERSION complete"
