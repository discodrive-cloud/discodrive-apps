#!/usr/bin/env bash
# End-to-end check for iOS auto-upload against a local DiscoDrive server.
#
# Needs one manual tap: iOS 26 asks for full photo-library access with a system alert that
# neither `simctl privacy` nor XCUITest can answer. The script stops and tells you when.
#
#   scripts/ios-e2e.sh                      # local docker server on :8080
#   DD_SERVER=http://localhost:18080 scripts/ios-e2e.sh
#
# It leaves the simulator paired to that server and prints what landed there.
set -euo pipefail

SERVER="${DD_SERVER:-http://localhost:8080}"
EMAIL="${DD_EMAIL:-iostest@local.test}"
PASSWORD="${DD_PASSWORD:-IosTest12345!}"
BUNDLE="org.discodrive.ios"
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

say() { printf '\n\033[1m== %s\033[0m\n' "$*"; }

# --- simulator ---------------------------------------------------------------
# `|| true` on every lookup: a pipeline whose grep matches nothing would otherwise take the
# whole script down through `set -euo pipefail`, before the fallback below gets a chance.
SIM="$(xcrun simctl list devices 2>/dev/null | grep '(Booted)' | head -1 |
       sed -E 's/.*\(([0-9A-Fa-f-]{36})\) \(Booted\).*/\1/' || true)"

if [ -z "${SIM:-}" ]; then
  say "no simulator is running — starting one"
  SIM="$(xcrun simctl list devices available 2>/dev/null |
         grep -E 'iPhone 1[6-9]|iPhone Air' | head -1 |
         sed -E 's/.*\(([0-9A-Fa-f-]{36})\).*/\1/' || true)"
  if [ -z "${SIM:-}" ]; then
    echo "no iOS simulator is installed — add one in Xcode ▸ Settings ▸ Components"
    exit 1
  fi
  xcrun simctl boot "$SIM" 2>/dev/null || true
  open -a Simulator --args -CurrentDeviceUDID "$SIM" 2>/dev/null || true
  # Booting takes a while, and the tests need a device that answers.
  for _ in $(seq 1 40); do
    state="$(xcrun simctl list devices 2>/dev/null | grep "$SIM" | grep -c Booted || true)"
    [ "$state" != "0" ] && break
    sleep 3
  done
fi
say "using simulator $SIM"
open -a Simulator 2>/dev/null || true

# --- server ------------------------------------------------------------------
curl -sf -m 5 "$SERVER/health" >/dev/null || { echo "no server at $SERVER"; exit 1; }

login() {
  curl -s -X POST "$SERVER/auth/login" -H 'Content-Type: application/json' \
    -d "{\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\"}" |
    python3 -c 'import sys,json; print(json.load(sys.stdin).get("token",""))'
}
JWT="$(login || true)"
if [ -z "$JWT" ]; then
  say "creating the test account $EMAIL"
  curl -s -X POST "$SERVER/auth/register" -H 'Content-Type: application/json' \
    -d "{\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\"}" >/dev/null || true
  JWT="$(login || true)"
  [ -n "$JWT" ] || { echo "cannot log in as $EMAIL — check the server at $SERVER"; exit 1; }
fi

say "pairing a device"
PAIR="$(curl -s -X POST "$SERVER/pair/init" -H 'Content-Type: application/json' \
  -d '{"name":"iOS Simulator (e2e)","kind":"ios"}')"
CODE="$(echo "$PAIR" | python3 -c 'import sys,json; print(json.load(sys.stdin)["user_code"])')"
DEVICE_CODE="$(echo "$PAIR" | python3 -c 'import sys,json; print(json.load(sys.stdin)["device_code"])')"
curl -s -X POST "$SERVER/pair/$CODE/approve" -H "Authorization: Bearer $JWT" \
  -H 'Content-Type: application/json' -d '{"name":"iOS Simulator (e2e)"}' >/dev/null
TOKEN="$(curl -s -X POST "$SERVER/pair/token" -H 'Content-Type: application/json' \
  -d "{\"device_code\":\"$DEVICE_CODE\"}" |
  python3 -c 'import sys,json; print(json.load(sys.stdin).get("device_token",""))')"
[ -n "$TOKEN" ] || { echo "pairing failed"; exit 1; }

# --- clean slate -------------------------------------------------------------
# Uninstalling clears the journal and the settings, so the seeding rule is checked for real;
# resetting privacy is what makes the permission alert appear again.
say "resetting the app on the simulator"
xcrun simctl uninstall "$SIM" "$BUNDLE" 2>/dev/null || true
xcrun simctl privacy "$SIM" reset all "$BUNDLE" 2>/dev/null || true

say "putting a few photos in the library (these must NOT be uploaded)"
python3 - "$WORK" <<'PY'
import struct, zlib, sys, os, random
out = sys.argv[1]
def png(path, w, h, seed):
    def chunk(t, d):
        c = t + d
        return struct.pack(">I", len(d)) + c + struct.pack(">I", zlib.crc32(c) & 0xffffffff)
    random.seed(seed)
    raw = b"".join(b"\x00" + bytes(random.getrandbits(8) for _ in range(w * 3)) for _ in range(h))
    open(path, "wb").write(b"\x89PNG\r\n\x1a\n"
                           + chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0))
                           + chunk(b"IDAT", zlib.compress(raw)) + chunk(b"IEND", b""))
for i in range(3):
    png(os.path.join(out, f"existing_{i}.png"), 400, 300, 1000 + i)
PY
xcrun simctl addmedia "$SIM" "$WORK"/existing_*.png

# --- phase 1: needs the tap --------------------------------------------------
cd "$REPO/clients/ios"
xcodegen generate >/dev/null

# Bring the simulator forward: the prompt is easy to miss behind the editor, and the run
# stalls for four minutes waiting for a tap nobody saw.
open -a Simulator 2>/dev/null || true
say "PHASE 1 — switching auto-upload on. YOU WILL BE ASKED TO TAP \"Allow Full Access\""
TEST_RUNNER_DD_TEST_SERVER="$SERVER" TEST_RUNNER_DD_TEST_TOKEN="$TOKEN" \
xcodebuild test -project DiscoDrive.xcodeproj -scheme DiscoDrive \
  -destination "platform=iOS Simulator,id=$SIM" -derivedDataPath build CODE_SIGNING_ALLOWED=NO \
  -only-testing:DiscoDriveUITests/AutoUploadUITests/test0_ScreenRendersBeforeEnabling \
  -only-testing:DiscoDriveUITests/AutoUploadUITests/test1_EnablingSeedsTheLibraryInsteadOfUploadingIt \
  2>&1 | grep -E "TAP |Allow Full Access|PHASE1|Test Case|XCTAssert|error:|TEST (SUCCEEDED|FAILED)" || true

# --- phase 2: unattended -----------------------------------------------------
say "adding one new photo — this is the one that must be uploaded"
python3 - "$WORK" <<'PY'
import struct, zlib, sys, os, random
out = sys.argv[1]
def png(path, w, h):
    def chunk(t, d):
        c = t + d
        return struct.pack(">I", len(d)) + c + struct.pack(">I", zlib.crc32(c) & 0xffffffff)
    raw = b"".join(b"\x00" + bytes(random.getrandbits(8) for _ in range(w * 3)) for _ in range(h))
    open(path, "wb").write(b"\x89PNG\r\n\x1a\n"
                           + chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0))
                           + chunk(b"IDAT", zlib.compress(raw)) + chunk(b"IEND", b""))
random.seed()
png(os.path.join(out, "fresh.png"), 900, 700)
PY
xcrun simctl addmedia "$SIM" "$WORK/fresh.png"

say "PHASE 2 — uploading the new photo (no tapping needed)"
TEST_RUNNER_DD_TEST_SERVER="$SERVER" TEST_RUNNER_DD_TEST_TOKEN="$TOKEN" \
xcodebuild test -project DiscoDrive.xcodeproj -scheme DiscoDrive \
  -destination "platform=iOS Simulator,id=$SIM" -derivedDataPath build CODE_SIGNING_ALLOWED=NO \
  -only-testing:DiscoDriveUITests/AutoUploadUITests/test2_NewPhotoIsUploaded \
  2>&1 | grep -E "PHASE2|Test Case|XCTAssert|error:|TEST (SUCCEEDED|FAILED)" || true

# --- what actually landed ----------------------------------------------------
say "what is on the server now"
python3 - "$SERVER" "$JWT" <<'PY'
import json, sys, urllib.request
server, jwt = sys.argv[1], sys.argv[2]
def get(path):
    req = urllib.request.Request(server + path, headers={"Authorization": "Bearer " + jwt})
    return json.load(urllib.request.urlopen(req))
root = next((n for n in get("/files") if n["name"] == "Camera Uploads"), None)
if not root:
    print("  no Camera Uploads folder — nothing was uploaded")
    raise SystemExit
for device in get(f"/files?parent_id={root['id']}"):
    print(f"  {device['name']}/")
    for f in get(f"/files?parent_id={device['id']}"):
        print(f"    {f['name']}  {f.get('size')} bytes  v{f['version']}  {f.get('modified_at','')[:19]}")
PY
