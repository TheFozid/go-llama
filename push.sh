#!/usr/bin/env bash
set -e

# Usage:
#   ./push.sh "commit message" patch
#   ./push.sh "commit message" minor
#   ./push.sh "commit message" major
#   ./push.sh "commit message" testing

MSG="$1"
BUMP="$2"

if [ -z "$MSG" ] || [ -z "$BUMP" ]; then
    echo "Usage: ./push.sh \"Commit message\" [patch|minor|major|testing]"
    exit 1
fi

IMAGE_BASE="ghcr.io/thefozid/go-llama"

# ----------------------------
# TESTING MODE (no version bump)
# ----------------------------
if [ "$BUMP" = "testing" ] || [ "$BUMP" = "test" ]; then
  echo "🧪 TESTING MODE ENABLED"
  echo

  # Git operations
  echo "📦 Git commit (no version tag)..."
  git add .
  git commit -m "$MSG" || echo "No changes to commit"

  # Use short git SHA for unique testing tag
  GIT_SHA=$(git rev-parse --short HEAD)
  TEST_TAG="testing-$GIT_SHA"

  echo
  echo "🚀 Building & pushing TESTING images..."
  docker buildx build \
    --platform linux/amd64 \
    --cache-from type=registry,ref="$IMAGE_BASE:buildcache" \
    --cache-to type=registry,ref="$IMAGE_BASE:buildcache",mode=min \
    --build-arg BUILDKIT_INLINE_CACHE=1 \
    -t "$IMAGE_BASE:testing" \
    -t "$IMAGE_BASE:$TEST_TAG" \
    --push .

  echo
  echo "🌐 Pushing git changes..."
  git push

  echo
  echo "🧹 Cleaning up old images..."
  docker system prune -f --filter "until=84h"
  docker buildx prune --filter "until=84h" -f

  echo
  echo "✅ Testing build complete:"
  echo "   - $IMAGE_BASE:testing"
  echo "   - $IMAGE_BASE:$TEST_TAG"
  exit 0
fi


# ----------------------------
# NORMAL RELEASE MODE
# ----------------------------

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
  *) echo "Invalid bump: $BUMP (use patch|minor|major|testing)"; exit 1 ;;
esac

VERSION="$MAJOR.$MINOR.$PATCH"

echo "📌 Previous version: $LATEST"
echo "✨ New version: v$VERSION"
echo

# Git operations BEFORE build
echo "📦 Git commit + tag..."
git add .
git commit -m "$MSG" || echo "No changes to commit"
git tag "v$VERSION"

# Build + push images
echo "🚀 Building & pushing multi-arch images..."
docker buildx build \
  --platform linux/amd64 \
  --cache-from type=registry,ref="$IMAGE_BASE:buildcache" \
  --cache-to type=registry,ref="$IMAGE_BASE:buildcache",mode=min \
  --build-arg BUILDKIT_INLINE_CACHE=1 \
  -t "$IMAGE_BASE:latest" \
  -t "$IMAGE_BASE:$VERSION" \
  -t "$IMAGE_BASE:$MAJOR.$MINOR" \
  -t "$IMAGE_BASE:$MAJOR" \
  --push .

echo
echo "🌐 Pushing git changes..."
git push
git push --tags

# Selective cleanup
echo "🧹 Cleaning up old images..."
docker system prune -f --filter "until=84h"
docker buildx prune --filter "until=84h" -f

echo
echo "✅ Release v$VERSION complete"
