#!/usr/bin/env bash
#
# Publish dist/ artifacts as a release on kzsh/gh-ff, with the version taken
# from the tag on HEAD.
#
# The tag is the release: build.sh stamps it into the binaries and this script
# uploads dist/ under it. Cut a release by tagging HEAD first:
#
#   git tag -a v0.1.0 -m 'gh-ff 0.1.0'
#   ./scripts/build.sh
#   ./scripts/publish.sh
#
# Each release carries every binary under a versioned and an unversioned
# name, plus SHA256SUMS; install.sh and the README fetch the unversioned
# names through GitHub's releases/latest/download redirect. The release is
# marked Latest only when its version is the highest stable one published, so
# a patch to an older line does not take over default installs. Versions with
# a pre-release suffix (0.2.0-rc1) are published as GitHub pre-releases and
# never marked Latest.
#
# Published versions are immutable: if the release already exists the script
# exits without uploading. It refuses a dirty tree, an HEAD that is not
# exactly tagged, and a dist/ stamped with anything other than HEAD's tag.
#
# Pushing the tag also pushes the commits it needs, so HEAD does not have to
# be on GitHub beforehand.
#
# Prerequisites:
#   - gh (GitHub CLI): https://cli.github.com, authenticated with gh auth login
#   - go, for the version-stamp check on the native artifact
#   - dist/ populated by scripts/build.sh
#
# Usage:
#   ./scripts/publish.sh

set -euo pipefail

REPO="kzsh/gh-ff"
DIST="${DIST:-dist}"

if [[ $# -gt 0 ]]; then
    echo "error: unknown argument: $1"
    exit 1
fi

command -v gh &>/dev/null || {
    echo "error: gh not found. Install from https://cli.github.com"
    exit 1
}

if [[ -n "$(git status --porcelain)" ]]; then
    echo "error: working tree has uncommitted changes; commit or stash before publishing."
    exit 1
fi

TAG="$(git describe --tags --exact-match HEAD 2>/dev/null || true)"
if [[ -z "$TAG" ]]; then
    echo "error: HEAD is not exactly tagged; tag it and rebuild before publishing."
    echo "       git tag -a v0.1.0 -m 'gh-ff 0.1.0' && ./scripts/build.sh"
    exit 1
fi
if [[ ! "$TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.]+)?$ ]]; then
    echo "error: tag $TAG is not a version tag (vX.Y.Z with an optional pre-release suffix)."
    exit 1
fi
VERSION="${TAG#v}"

if gh release view "$TAG" --repo "$REPO" &>/dev/null; then
    echo "$TAG already exists on $REPO; cut a new tag to publish. Nothing uploaded."
    exit 0
fi

mapfile -t unversioned < <(find "$DIST" -maxdepth 1 -name "gh-ff-*" -not -name "gh-ff-$VERSION-*" -type f | sort)

if [[ ${#unversioned[@]} -eq 0 ]]; then
    echo "error: no artifacts found in $DIST/. Run scripts/build.sh first."
    exit 1
fi

# Catches a dist/ left over from another commit: the stamp build.sh injected
# has to read exactly the tag being published, so a build from a dirty tree or
# a later commit fails here rather than shipping under this tag. Only the
# artifact for this host runs; the rest are checked by nothing but their sums.
if command -v go &>/dev/null; then
    native="$DIST/gh-ff-$(go env GOOS)-$(go env GOARCH)"
    if [[ -x "$native" ]]; then
        stamped="$("$native" --version || true)"
        case "$stamped" in
            "gh-ff $TAG "*) ;;
            *)
                echo "error: $native reports '$stamped', not $TAG; rebuild dist/ from HEAD."
                exit 1
                ;;
            esac
    fi
else
    echo "note: go not found; skipping the version-stamp check."
fi

versioned=()
for f in "${unversioned[@]}"; do
    base="$(basename "$f")"
    dest="$DIST/gh-ff-$VERSION-${base#gh-ff-}" # gh-ff-linux-amd64 -> gh-ff-0.1.0-linux-amd64
    cp "$f" "$dest"
    versioned+=("$dest")
done
artifacts=("${versioned[@]}" "${unversioned[@]}")

# install.sh refuses any release without this file. Bare filenames, so the
# entries match asset names as uploaded.
(
    cd "$DIST"
    for f in "${artifacts[@]}"; do basename "$f"; done | xargs sha256sum
) > "$DIST/SHA256SUMS"
artifacts+=("$DIST/SHA256SUMS")

# GitHub marks the newest release Latest by default, whatever its version.
# sort -V orders x.y.z correctly but places 1.0.0-rc1 after 1.0.0, so only
# suffix-free versions take part in the comparison.
if [[ "$VERSION" == *-* ]]; then
    release_flags=(--prerelease --latest=false)
else
    highest="$(
        {
            gh release list --repo "$REPO" --limit 1000 \
                --exclude-drafts --exclude-pre-releases \
                --json tagName -q '.[].tagName'
            echo "$TAG"
        } | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | sort -V | tail -1
    )"
    if [[ "$highest" == "$TAG" ]]; then
        release_flags=(--latest)
    else
        release_flags=(--latest=false)
    fi
fi

# The tag carries its commits with it, so the release can point at HEAD even
# when HEAD has never been pushed.
gh_remote=""
for remote in $(git remote); do
    url="$(git remote get-url "$remote")"
    if [[ "$url" =~ github\.com[:/]$REPO(\.git)?$ ]]; then
        gh_remote="$remote"
        break
    fi
done
if [[ -n "$gh_remote" ]]; then
    if ! git ls-remote --exit-code --tags "$gh_remote" "refs/tags/$TAG" >/dev/null 2>&1; then
        echo "==> pushing $TAG to $gh_remote"
        git push "$gh_remote" "refs/tags/$TAG"
    fi
else
    echo "note: no remote for $REPO; the tag stays local."
fi

echo "Tag:        $TAG"
echo "Repo:       $REPO"
echo "Flags:      ${release_flags[*]}"
echo "Artifacts:"
for f in "${artifacts[@]}"; do
    printf "  %s\n" "$(basename "$f")"
done
echo ""

gh release create "$TAG" \
    --repo "$REPO" \
    --target "$(git rev-parse HEAD)" \
    --title "$TAG" \
    --notes '' \
    "${release_flags[@]}" \
    "${artifacts[@]}"

echo "Published: https://github.com/$REPO/releases/tag/$TAG"
