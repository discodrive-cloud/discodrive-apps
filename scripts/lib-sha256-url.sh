# Sourced by the Homebrew generators. sha256_url downloads to a file first: hashing a
# curl pipe would, on a failed download, hash empty input (e3b0c442…) and publish it.
sha256_url() {
  _f=$(mktemp)
  if ! curl -fsSL -o "$_f" "$1"; then
    rm -f "$_f"; echo "sha256_url: download failed: $1" >&2; exit 1
  fi
  if [ ! -s "$_f" ]; then
    rm -f "$_f"; echo "sha256_url: empty download: $1" >&2; exit 1
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    _h=$(sha256sum "$_f" | cut -d' ' -f1)
  else
    _h=$(shasum -a 256 "$_f" | cut -d' ' -f1)
  fi
  rm -f "$_f"
  echo "$_h"
}
