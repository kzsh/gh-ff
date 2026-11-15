#!/usr/bin/env bash
#
# Empty dist/ so the next build ships only what the current commit compiled.
#
# publish.sh uploads every gh-ff-* file it finds and stamps nothing, so an
# artifact left by an older release rides along to the next publish: it is
# renamed under the new version and passes the version-stamp check, which
# inspects only the artifact for the publishing host.
#
# Usage:
#   ./scripts/clean.sh
#   DIST=out ./scripts/clean.sh

set -euo pipefail

DIST="${DIST:-dist}"

if [[ $# -gt 0 ]]; then
    echo "error: unknown argument: $1"
    exit 1
fi

rm -rf "$DIST"
echo "cleaned: $DIST/"
