#!/bin/sh
set -eu

git fetch --tags --force

LAST="$(git tag -l 'v[0-9]*.[0-9]*.[0-9]*' --sort=-version:refname | head -n 1)"
[ -n "$LAST" ] || { echo "no semver tag found, expected vX.Y.Z" >&2; exit 1; }

ver="${LAST#v}"

OLDIFS=$IFS
IFS=.
set -- $ver
IFS=$OLDIFS

MAJOR=${1:-}
MINOR=${2:-}
PATCH=${3:-}

case "$MAJOR.$MINOR.$PATCH" in
	[0-9]*.[0-9]*.[0-9]*) ;;
	*) echo "bad LAST: '$LAST'" >&2; exit 1 ;;
esac

PATCH=$((PATCH + 1))
NEW="v${MAJOR}.${MINOR}.${PATCH}"

case "$NEW" in
	v[0-9]*.[0-9]*.[0-9]*) ;;
	*) echo "bad tag: '$NEW' (LAST='$LAST')" >&2; exit 1 ;;
esac

git add .

git commit -m "$NEW" || true

git tag "$NEW"

git push
git push origin "$NEW"
