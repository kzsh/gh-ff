#!/bin/sh
#
# gh-ff install script
# Downloads the matching binary from GitHub releases.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/kzsh/gh-ff/main/install.sh | sh
#   curl -fsSL .../install.sh | sh -s -- --release 0.1.0
#
# Environment variables:
#   GH_FF_RELEASE       Release to install: latest, or a version like 0.1.0
#                       (default: latest). Overridden by --release.
#   GH_FF_INSTALL_DIR   Directory to install the gh-ff binary into
#                       (default: ~/.local/bin).

set -eu

RELEASE="${GH_FF_RELEASE:-latest}"
BIN_DIR="${GH_FF_INSTALL_DIR:-$HOME/.local/bin}"
BIN_PATH="$BIN_DIR/gh-ff"
REPO="kzsh/gh-ff"

path_action="already"
path_profile=""
tmp_file=""

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

step() {
    printf '==> %s\n' "$1"
}

warn() {
    printf 'WARNING: %s\n' "$1" >&2
}

die() {
    printf 'error: %s\n' "$1" >&2
    exit 1
}

download_file() {
    url="$1"
    output="$2"

    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$url" -o "$output"
        return
    fi

    if command -v wget >/dev/null 2>&1; then
        wget -q -O "$output" "$url"
        return
    fi

    die "curl or wget is required to install gh-ff."
}

file_sha256() {
    path="$1"

    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$path" | awk '{print $1}'
        return
    fi

    if command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$path" | awk '{print $1}'
        return
    fi

    if command -v openssl >/dev/null 2>&1; then
        openssl dgst -sha256 "$path" | sed 's/^.*= //'
        return
    fi

    die "sha256sum, shasum, or openssl is required to verify the download."
}

# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------

parse_args() {
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --release)
                if [ "$#" -lt 2 ]; then
                    die "--release requires a value."
                fi
                RELEASE="$2"
                shift
                ;;
            --help | -h)
                cat <<EOF
Usage: install.sh [--release VERSION]

Install gh-ff from GitHub releases.

Options:
  --release VERSION   Install a specific release (e.g. 0.1.0, v0.1.0, latest).
  --help, -h          Show this message.

Environment variables:
  GH_FF_RELEASE        Release to install; overridden by --release.
  GH_FF_INSTALL_DIR    Directory for the binary (default: ~/.local/bin).
EOF
                exit 0
                ;;
            *)
                die "Unknown argument: $1"
                ;;
        esac
        shift
    done
}

# ---------------------------------------------------------------------------
# Version normalization
# ---------------------------------------------------------------------------

# Strips a leading 'v' and returns the bare version number, or "latest".
normalize_version() {
    case "$1" in
        "" | latest)
            printf 'latest\n'
            ;;
        v*)
            printf '%s\n' "${1#v}"
            ;;
        *)
            printf '%s\n' "$1"
            ;;
    esac
}

validate_version() {
    version="$1"

    if [ "$version" = "latest" ]; then
        return
    fi

    if ! printf '%s\n' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.]+)?$'; then
        die "Invalid release version: $version. Expected latest or x.y.z (with optional pre-release suffix)."
    fi
}

# ---------------------------------------------------------------------------
# Shell profile for PATH updates
# ---------------------------------------------------------------------------

# Returns the most appropriate shell profile file to append PATH updates to.
pick_profile() {
    case "$os:${SHELL:-}" in
        darwin:*/zsh)
            printf '%s\n' "$HOME/.zprofile"
            ;;
        darwin:*/bash)
            printf '%s\n' "$HOME/.bash_profile"
            ;;
        linux:*/zsh)
            printf '%s\n' "$HOME/.zshrc"
            ;;
        linux:*/bash)
            printf '%s\n' "$HOME/.bashrc"
            ;;
        *)
            printf '%s\n' "$HOME/.profile"
            ;;
    esac
}

add_to_path() {
    path_action="already"
    path_profile=""

    case ":${PATH}:" in
        *":$BIN_DIR:"*)
            return
            ;;
    esac

    profile="$(pick_profile)"
    path_profile="$profile"
    begin_marker="# >>> gh-ff installer >>>"
    end_marker="# <<< gh-ff installer <<<"
    path_line="export PATH=\"$BIN_DIR:\$PATH\""

    if [ -f "$profile" ] && grep -qF "$begin_marker" "$profile" 2>/dev/null; then
        if grep -qF "$path_line" "$profile" 2>/dev/null; then
            path_action="configured"
            return
        fi
        path_action="updated"
        rewrite_path_block "$profile" "$begin_marker" "$end_marker" "$path_line"
        return
    fi

    {
        printf '\n%s\n' "$begin_marker"
        printf '%s\n' "$path_line"
        printf '%s\n' "$end_marker"
    } >>"$profile"
    path_action="added"
}

rewrite_path_block() {
    profile="$1"
    begin_marker="$2"
    end_marker="$3"
    path_line="$4"

    tmp_profile="$tmp_file.profile"
    awk -v begin="$begin_marker" -v end="$end_marker" -v line="$path_line" '
        BEGIN { in_block = 0; replaced = 0 }
        $0 == begin {
            if (!replaced) {
                print begin
                print line
                print end
                replaced = 1
            }
            in_block = 1
            next
        }
        in_block {
            if ($0 == end) { in_block = 0 }
            next
        }
        { print }
        END { if (in_block != 0) { exit 1 } }
    ' "$profile" >"$tmp_profile"
    mv "$tmp_profile" "$profile"
}

print_launch_instructions() {
    case "$path_action" in
        added)
            step "Current terminal: export PATH=\"$BIN_DIR:\$PATH\" && gh-ff"
            step "Future terminals: open a new terminal and run: gh-ff"
            step "PATH was added to $path_profile"
            ;;
        updated)
            step "Current terminal: export PATH=\"$BIN_DIR:\$PATH\" && gh-ff"
            step "Future terminals: open a new terminal and run: gh-ff"
            step "PATH was updated in $path_profile"
            ;;
        configured)
            step "PATH is already configured in $path_profile"
            step "Run: gh-ff"
            ;;
        *)
            step "$BIN_DIR is already on PATH"
            step "Run: gh-ff"
            ;;
    esac
}

# ---------------------------------------------------------------------------
# Cleanup
# ---------------------------------------------------------------------------

cleanup() {
    if [ -n "$tmp_file" ] && [ -f "$tmp_file" ]; then
        rm -f "$tmp_file"
    fi
}

trap cleanup EXIT INT TERM

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

parse_args "$@"

# Detect OS
case "$(uname -s)" in
    Darwin)
        os="darwin"
        platform_label="macOS"
        ;;
    Linux)
        os="linux"
        platform_label="Linux"
        ;;
    MINGW* | MSYS* | CYGWIN*)
        die "Windows is not supported by this script."
        ;;
    *)
        die "Unsupported operating system: $(uname -s)"
        ;;
esac

# Detect architecture, naming it as go's GOARCH does: the asset names come
# from scripts/build.sh, which names targets by GOOS-GOARCH.
case "$(uname -m)" in
    x86_64 | amd64)
        arch="amd64"
        ;;
    arm64 | aarch64)
        arch="arm64"
        ;;
    *)
        die "Unsupported architecture: $(uname -m)"
        ;;
esac

# On macOS, detect Rosetta 2: if the shell is running as x86_64 under
# Rosetta on an ARM Mac, take the native arm64 binary instead.
if [ "$os" = "darwin" ] && [ "$arch" = "amd64" ]; then
    if [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || true)" = "1" ]; then
        arch="arm64"
    else
        # Apple silicon only, matching scripts/build.sh. Without this the
        # Intel Mac user gets a 404 on gh-ff-darwin-amd64.
        die "No macOS (Intel) binary is published; gh-ff requires Apple silicon."
    fi
fi

# Resolve release and asset name. Every release (tag "v0.1.0") carries each
# binary twice:
#   gh-ff-<goos>-<goarch>         unversioned
#   gh-ff-0.1.0-<goos>-<goarch>   versioned (no leading 'v' in the filename)
#
# "latest" goes through GitHub's releases/latest/download redirect, which
# follows the release marked Latest; publish.sh marks the highest version.
# If a publish lands between the binary and SHA256SUMS downloads, the two
# come from different releases and the checksum check aborts the install.
version="$(normalize_version "$RELEASE")"
validate_version "$version"

if [ "$version" = "latest" ]; then
    tag="latest"
    asset="gh-ff-$os-$arch"
    release_url="https://github.com/$REPO/releases/latest/download"
else
    tag="v$version"
    asset="gh-ff-$version-$os-$arch"
    release_url="https://github.com/$REPO/releases/download/$tag"
fi

download_url="$release_url/$asset"

# Print plan.
step "Installing gh-ff"
step "Detected platform: $platform_label ($arch)"
step "Release:           $tag"
step "Downloading from:  $download_url"

# Download to a temp file.
tmp_file="$(mktemp)"
if ! download_file "$download_url" "$tmp_file"; then
    die "Download failed. Check that release '$tag' exists at https://github.com/$REPO/releases"
fi

# Sanity-check: reject obvious HTML error pages.
first_bytes="$(head -c 15 "$tmp_file" 2>/dev/null || true)"
case "$first_bytes" in
    "<!DOCTYPE html"* | "<html"*)
        die "Download returned an HTML page instead of a binary. The release '$tag' or asset '$asset' may not exist."
        ;;
esac

# Verify against the release's SHA256SUMS, written by scripts/publish.sh.
# Missing sums, an unlisted asset, or a mismatch all abort the install.
# This catches corruption and truncation; it does not authenticate the
# release, since the sums come from the same place as the binary.
checksums_url="$release_url/SHA256SUMS"
checksums_file="$(mktemp)"
if ! download_file "$checksums_url" "$checksums_file" 2>/dev/null; then
    rm -f "$checksums_file" "$tmp_file"
    die "Release '$tag' has no SHA256SUMS; refusing to install an unverified binary."
fi
# sha256sum marks binary-mode entries with a '*' before the filename.
expected="$(awk -v a="$asset" '$2 == a || $2 == "*" a { print $1; exit }' "$checksums_file")"
rm -f "$checksums_file"
if [ -z "$expected" ]; then
    rm -f "$tmp_file"
    die "SHA256SUMS for '$tag' does not list $asset."
fi
actual="$(file_sha256 "$tmp_file")"
if [ "$actual" != "$expected" ]; then
    rm -f "$tmp_file"
    die "Checksum mismatch for $asset. Expected: $expected  Got: $actual"
fi
step "Checksum verified"

# Install the binary.
mkdir -p "$BIN_DIR"
chmod 0755 "$tmp_file"
mv "$tmp_file" "$BIN_PATH"
tmp_file=""

step "Installed gh-ff to $BIN_PATH"

# Stamped by scripts/build.sh, so --version also reports which release
# landed. The bytes are already pinned by the checksum above.
installed="$("$BIN_PATH" --version 2>/dev/null || true)"
if [ -z "$installed" ]; then
    warn "gh-ff was installed but 'gh-ff --version' failed. The binary may not be compatible with this system."
fi

add_to_path
print_launch_instructions

printf '%s installed successfully.\n' "${installed:-$tag}"
