#!/usr/bin/env python3
"""Allowlist-check and extract unsigned-macos.tar for the release workflow.

Usage: verify-unsigned-macos-tar.py <path-to-tar> <destination-dir>

Validates every member of the archive, then extracts it — using the SAME parser
(Python's tarfile module) for both steps, into the SAME destination. Earlier revisions
of this script only validated with tarfile and then extracted separately with bsdtar
(`tar -xf`) in the workflow; that is a verifier/extractor mismatch and is exploitable:
tarfile silently stops at the first malformed header and reports only the members before
it (exit 0, no error), while bsdtar prints "Damaged tar archive / Retrying...", skips
past the junk, and keeps extracting whatever comes after it. A crafted archive — one
allowlisted entry, then deliberately corrupted bytes, then a hidden entry such as
".git/config" with a malicious "core.fsmonitor" command — passed the old validation step
(tarfile saw only the one good entry) while bsdtar's extraction step wrote the hidden
entry to disk anyway, where the next step's own "git diff"/"git status" tripwire call
would trigger it. This script never shells out to tar at all, for listing or extraction,
and independently rejects any archive that has trailing bytes after what tarfile
recognizes as its last member (see check_no_trailing_data) — precisely the shape of that
attack — even before considering path or type.

Exits 1 (with an ::error:: line for the Actions log) on the first problem found:
  - fewer than one member, or trailing bytes after the last recognized member (a
    malformed/truncated/"resync" archive — see above)
  - a member whose type is not exactly a plain regular file (REGTYPE/AREGTYPE) or a
    directory (DIRTYPE), or that is a GNU sparse member (symlinks and
    hardlinks are rejected outright: the real Wails-built DiscoDrive.app has none, so
    there is no legitimate reason for either to be in this archive, and both are classic
    vectors for making an "allowlisted" path resolve somewhere else — a chain of relative
    symlinks can walk out of the bundle one hop at a time even when each individual link's
    literal text looks contained, and a hardlink's `linkname` is resolved against the
    archive root, not the entry's own directory, so a naive same-directory check on it is
    wrong)
  - a path that is absolute, contains a ".." segment, or is not exactly one of the four
    daemon binaries or under the DiscoDrive.app bundle
  - the destination extraction is only ever done with `filter="data"` (str, not a
    reference to a TarFilter enum member) via `TarFile.extractall`, which is the standard
    library's own safe-extraction filter (Python 3.12+): it independently strips
    device/fifo entries, absolute paths, and symlinks that would land outside the
    destination. This script requires Python 3.12+ and refuses to run at all otherwise —
    there is no "extract without the filter" fallback path.

Called from the sign-macos job in .github/workflows/release.yml, before the signing key
is ever imported.
"""
import posixpath
import sys
import tarfile

ALLOWED_FILES = {
    "dist/darwin-amd64/discodrive",
    "dist/darwin-arm64/discodrive",
    "dist/darwin-amd64-tray/discodrive",
    "dist/darwin-arm64-tray/discodrive",
}
APP_ROOT = "daemon/cmd/discodrive-wails/build/bin/DiscoDrive.app"
APP_PREFIX = APP_ROOT + "/"

BLOCKSIZE = 512


def fail(msg):
    print(f"::error::{msg}", file=sys.stderr)
    sys.exit(1)


def require_python_312_with_data_filter():
    if sys.version_info < (3, 12):
        fail(
            f"python {sys.version.split()[0]} is too old: this script requires 3.12+ "
            "for tarfile's built-in extraction filter (no less-safe fallback exists)"
        )
    if not hasattr(tarfile, "data_filter"):
        fail(
            "tarfile.data_filter is missing even though python is 3.12+; refusing to "
            "extract without it"
        )


def check_path(name):
    if posixpath.isabs(name) or ".." in name.split("/"):
        fail(f"unsafe path: {name}")
    bare = name.rstrip("/")
    in_app = bare == APP_ROOT or name.startswith(APP_PREFIX)
    if not (in_app or name in ALLOWED_FILES):
        fail(f"disallowed path: {name}")


def check_type(m):
    if m.issym():
        fail(f"symlinks are not allowed in this archive: {m.name} -> {m.linkname}")
    if m.islnk():
        fail(f"hardlinks are not allowed in this archive: {m.name} -> {m.linkname}")
    if m.issparse():
        # GNU sparse members report isreg() but carry a hole map whose meaning differs
        # between extractors; the real build output never produces them.
        fail(f"sparse members are not allowed in this archive: {m.name}")
    if m.type not in (tarfile.REGTYPE, tarfile.AREGTYPE, tarfile.DIRTYPE):
        fail(f"disallowed tar entry type ({m.type!r}) for {m.name}")


def check_no_trailing_data(path, members):
    """Reject any archive with non-zero bytes after the last member tarfile parsed.

    A well-formed tar file's content ends with all-zero padding (the two 512-byte
    end-of-archive blocks, plus whatever zero padding fills out the blocking factor).
    Bytes tarfile did not parse into a member, that are not all zero, mean either archive
    corruption or — as in the attack this script exists to close — a second archive
    (or loose entries) concatenated after a deliberately damaged header, which a lenient
    extractor like bsdtar will happily resync into and extract, while tarfile silently
    stopped short and reported success on the truncated prefix.
    """
    last_end = max(m.offset_data + m.size for m in members)
    pad_end = ((last_end + BLOCKSIZE - 1) // BLOCKSIZE) * BLOCKSIZE
    with open(path, "rb") as f:
        f.seek(pad_end)
        rest = f.read()
    if any(b != 0 for b in rest):
        fail(
            f"{path} has non-zero bytes after its last recognized member "
            f"(offset {pad_end}, {len(rest)} trailing bytes) — treating as a "
            "malformed/tampered archive, not attempting to parse further"
        )


def verify_and_extract(path, dest):
    require_python_312_with_data_filter()
    with tarfile.open(path) as tf:
        members = tf.getmembers()
        if not members:
            fail(f"{path} is empty")
        for m in members:
            check_type(m)
            check_path(m.name)
        check_no_trailing_data(path, members)
        for m in members:
            print(f"  {m.name}")
        print(f"{path}: {len(members)} entries, all allowlisted — extracting to {dest}")
        tf.extractall(path=dest, members=members, filter="data")


if __name__ == "__main__":
    if len(sys.argv) != 3:
        print("usage: verify-unsigned-macos-tar.py <path-to-tar> <destination-dir>", file=sys.stderr)
        sys.exit(2)
    verify_and_extract(sys.argv[1], sys.argv[2])
