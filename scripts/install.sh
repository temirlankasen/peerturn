#!/bin/sh
# Save and inspect this script before running it. No unverified code is executed.
set -eu
umask 077

version=0.1.0-preview
repository=https://github.com/temirlankasen/peerturn
base=$repository/releases/download
verify_key=
lab=false
fail() { printf '%s\n' "peerturn installer: $*" >&2; exit 1; }
while [ "$#" -gt 0 ]; do
    case $1 in
        --lab) lab=true; shift ;;
        --verify-key)
            [ "$#" -ge 2 ] || fail 'missing explicitly trusted laboratory public key'
            [ -n "$2" ] || fail 'missing explicitly trusted laboratory public key'
            verify_key=$2; shift 2 ;;
        *) fail 'usage: sh install.sh [--lab [--verify-key TRUSTED_LAB_PUBLIC_KEY]]' ;;
    esac
done
if [ "${PEERTURN_RELEASE_BASE_URL+x}" = x ]; then
    "$lab" || fail 'PEERTURN_RELEASE_BASE_URL requires --lab'
    base=$PEERTURN_RELEASE_BASE_URL
fi
if [ -n "$verify_key" ]; then
    "$lab" || fail '--verify-key requires --lab'
    [ -f "$verify_key" ] || fail 'trusted laboratory public key is unavailable'
fi
case $base in https://*) ;; *) fail 'release source must use HTTPS' ;; esac
case $base in *'?'*|*'#'*|*'@'*|*' '*|*'
'*) fail 'invalid release source' ;; esac
if "$lab"; then
    printf '%s\n' 'WARNING: LABORATORY MODE: release source and publisher trust may be replaced.' \
        'Only independently trusted lab inputs are allowed; never use invitation or relay instructions.' >&2
fi
printf 'Release source: %s/v%s\n' "${base%/}" "$version" >&2
if [ -n "$verify_key" ]; then
    printf 'Publisher trust: explicit local cosign key %s; no public OIDC/transparency claim.\n' "$verify_key" >&2
else
    printf 'Publisher trust: GitHub OIDC %s/.github/workflows/release.yml@refs/tags/v%s (issuer https://token.actions.githubusercontent.com).\n' "$repository" "$version" >&2
fi
[ -n "${HOME:-}" ] || fail 'HOME is required'
case $HOME in /*) ;; *) fail 'HOME must be absolute' ;; esac
for tool in curl cosign tar awk sort find mktemp cp chmod mv mkdir; do
    command -v "$tool" >/dev/null 2>&1 || fail "required tool unavailable: $tool"
done
case $(uname -s) in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *) fail 'unsupported OS; release supports Linux and macOS only' ;;
esac
case $(uname -m) in
    x86_64|amd64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) fail 'unsupported architecture' ;;
esac
sha256() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print $1}'
    else
        fail 'sha256sum or shasum is required'
    fi
}
work=$(mktemp -d "${TMPDIR:-/tmp}/peerturn-install.XXXXXXXX")
trap 'rm -rf "$work"' 0
trap 'exit 1' HUP INT TERM
cosign version --json > "$work/cosign-version"
cosign_version=$(awk -F '"' '$2 == "gitVersion" {print $4}' "$work/cosign-version")
printf '%s\n' "$cosign_version" | awk '
  /^v[0-9]+\.[0-9]+\.[0-9]+$/ {
    sub(/^v/, ""); split($0, v, ".")
    if (v[1] > 3 || (v[1] == 3 && (v[2] > 1 || (v[2] == 1 && v[3] >= 3)))) ok=1
  }
  END {exit !ok}
' || fail 'cosign >=3.1.3 is required (legacy bundle verification fix)'
artifact=peerturn_${version}_${os}_${arch}.tar.gz
manifest=peerturn_${version}_checksums.txt
download() {
    curl -q --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
        --connect-timeout 15 --max-time 300 --max-filesize 134217728 \
        --output "$work/$1" "${base%/}/v$version/$1"
}
download "$manifest"
download "$manifest.sigstore.json"
if [ -n "$verify_key" ]; then
    cosign verify-blob --key "$verify_key" --bundle "$work/$manifest.sigstore.json" \
        --insecure-ignore-tlog=true "$work/$manifest" || fail 'signature verification failed'
else
    cosign verify-blob --bundle "$work/$manifest.sigstore.json" \
        --certificate-identity "$repository/.github/workflows/release.yml@refs/tags/v$version" \
        --certificate-oidc-issuer https://token.actions.githubusercontent.com \
        "$work/$manifest" || fail 'publisher signature verification failed'
fi
expected=$(awk -v name="$artifact" '$2 == name {n++; hash=$1} END {if (n != 1) exit 1; print hash}' "$work/$manifest") || fail 'archive is not uniquely listed'
[ "${#expected}" -eq 64 ] || fail 'invalid SHA-256 entry'
case $expected in *[!0-9a-f]*) fail 'invalid SHA-256 entry' ;; esac
download "$artifact"
[ "$(sha256 "$work/$artifact")" = "$expected" ] || fail 'archive checksum mismatch'

# Never extract links, devices, absolute paths, traversal, or unrecognized content.
tar -tzf "$work/$artifact" > "$work/members"
tar -tvzf "$work/$artifact" > "$work/types"
awk 'substr($0,1,1) != "-" && substr($0,1,1) != "d" {bad=1} END {exit bad}' "$work/types" || fail 'archive contains nonregular entries'
awk '
  /^\// || /(^|\/)\.\.?(\/|$)/ || /[^A-Za-z0-9._\/-]/ {bad=1}
  !/^(peerturn|LICENSE|NOTICE|README.md|\.claude-plugin\/|\.agents\/|adapters\/)/ {bad=1}
  {if (seen[$0]++) bad=1; if (++n > 256) bad=1}
  END {exit bad || n == 0}
' "$work/members" || fail 'archive has unsafe or duplicate paths'
mkdir "$work/package"
tar -xzf "$work/$artifact" -C "$work/package"
[ -f "$work/package/peerturn" ] || fail 'archive has no executable'
[ -f "$work/package/.claude-plugin/marketplace.json" ] || fail 'archive has no Claude marketplace'
[ -f "$work/package/.agents/plugins/marketplace.json" ] || fail 'archive has no Codex marketplace'
mkdir -p "$work/package/adapters/codex/plugin/bin"
cp "$work/package/peerturn" "$work/package/adapters/codex/plugin/bin/peerturn"
chmod 755 "$work/package/peerturn" "$work/package/adapters/codex/plugin/bin/peerturn" \
    "$work/package/adapters/codex/plugin/scripts/inform.sh"
(
    cd "$work/package"
    find . -type f | sort | while IFS= read -r file; do
        printf '%s  %s\n' "$(sha256 "$file")" "${file#./}"
    done
) > "$work/inventory"
mv "$work/inventory" "$work/package/.peerturn-files.sha256"
sha256 "$work/package/.peerturn-files.sha256" > "$work/package/.peerturn-verified"

destination=$HOME/.local/share/peerturn/$version
bindir=$HOME/.local/bin
# The same-UID OS account is trusted, but refuse preexisting symbolic links.
for path in "$HOME/.local" "$HOME/.local/share" "$HOME/.local/share/peerturn" "$destination" "$bindir" "$bindir/peerturn"; do
    [ ! -L "$path" ] || fail 'installation path is a symbolic link'
done
mkdir -p "$HOME/.local/share/peerturn" "$bindir"
[ ! -e "$destination" ] || fail 'version already installed; inspect and remove the old package explicitly before reinstalling'
mv "$work/package" "$destination"
cp "$destination/peerturn" "$work/peerturn"
chmod 755 "$work/peerturn"
mv "$work/peerturn" "$bindir/peerturn"
printf '%s\n' "Installed verified PeerTurn $version into ~/.local/bin." \
    'Add ~/.local/bin to PATH, then run peerturn setup claude or peerturn setup codex.' \
    'Setup prints required reload/resume, hook trust, and current-session binding steps.'
