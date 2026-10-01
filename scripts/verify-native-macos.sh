#!/usr/bin/env bash
# Reject development/test bundles before handing a native macOS build to a user.
set -euo pipefail
app="${1:?usage: $0 <DiscoDrive.app>}"
/usr/bin/python3 - "$app" <<'PY'
import pathlib, plistlib, subprocess, sys
app = pathlib.Path(sys.argv[1])
with (app / 'Contents/Info.plist').open('rb') as stream:
    info = plistlib.load(stream)
if info.get('CFBundleIdentifier') != 'org.discodrive.app':
    sys.exit('FAIL: not the DiscoDrive app identity (org.discodrive.app).')
extensions = sorted((app / 'Contents/PlugIns').glob('*.appex'))
identifiers = set()
for extension in extensions:
    with (extension / 'Contents/Info.plist').open('rb') as stream:
        identifiers.add(plistlib.load(stream).get('CFBundleIdentifier'))
if identifiers != {'org.discodrive.app.fileprovider', 'org.discodrive.app.fileproviderui'}:
    sys.exit('FAIL: missing or mismatched Release File Provider extensions.')
for bundle in [app, *extensions]:
    subprocess.run(['codesign', '--verify', '--strict', str(bundle)], check=True)
    details = subprocess.check_output(['codesign', '-dvv', str(bundle)], stderr=subprocess.STDOUT).decode()
    if 'Authority=Developer ID Application:' not in details:
        sys.exit(f'FAIL: {bundle.name} is not signed for Developer ID distribution.')
    ent = plistlib.loads(subprocess.check_output(['codesign', '-d', '--entitlements', ':-', str(bundle)], stderr=subprocess.DEVNULL))
    if ent.get('com.apple.security.get-task-allow') or '.debug' in str(ent):
        sys.exit(f'FAIL: {bundle.name} contains development entitlements.')
    profile = bundle / 'Contents/embedded.provisionprofile'
    if profile.exists():
        data = plistlib.loads(subprocess.check_output(['security', 'cms', '-D', '-i', str(profile)], stderr=subprocess.DEVNULL))
        if not data.get('ProvisionsAllDevices'):
            sys.exit(f'FAIL: {bundle.name} has a device-restricted provisioning profile.')
print('PASS: Release identity and Developer ID signatures for app and extensions.')
PY
codesign --verify --deep --strict "$app"
spctl --assess --type execute --verbose=2 "$app"
