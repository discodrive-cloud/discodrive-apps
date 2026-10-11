# discodrive-apps — unified build orchestrator.
# Run from the repo root. `make help` lists targets; `make doctor` checks toolchains.
# Override per-invocation: make daemon OS=windows ARCH=amd64 TRAY=1

DAEMON_DIR := daemon
DIST       := dist
HOST_OS    := $(shell go env GOOS)
OS   ?= $(HOST_OS)
ARCH ?= $(shell go env GOARCH)
TRAY ?= 0
# Release version x.y.z for the native macOS app and its dmg; required by dmg-macos*.
VERSION ?=

ifeq ($(TRAY),1)
  DAEMON_TAGS := -tags tray
  # fyne systray needs cgo only on darwin (AppKit); Linux (D-Bus) and Windows
  # (Win32) tray builds are pure Go and cross-compile freely.
  DAEMON_CGO  := $(if $(filter darwin,$(OS)),1,0)
else
  DAEMON_TAGS :=
  DAEMON_CGO  := 0
endif

EXE := $(if $(filter windows,$(OS)),.exe,)
# Tray builds get their own dist dir so both daemon flavors can coexist.
OUT := $(DIST)/$(OS)-$(ARCH)$(if $(filter 1,$(TRAY)),-tray,)

GRADLE_DD := $(if $(wildcard clients/android-discodrive/gradlew),./gradlew,gradle)
GRADLE_FS := $(if $(wildcard clients/android-fastsync/gradlew),./gradlew,gradle)

.PHONY: help daemon daemon-all \
        bind-ios bind-android \
        app-macos app-macos-unsigned dmg-macos dmg-macos-package ios-archive ios-upload \
        app-ios app-ios-fastsync app-android app-android-fastsync \
        desktop-linux desktop-windows \
        all-go test test-go test-swift doctor clean

help:
	@echo "discodrive-apps — build targets (override OS=, ARCH=, TRAY=1):"
	@echo "  daemon                headless sync daemon for OS/ARCH  (TRAY=1 ⇒ menubar/tray flavor)"
	@echo "  daemon-all            released headless daemon: linux amd64+arm64"
	@echo "  desktop-linux         desktop client Linux build via Debian Docker image"
	@echo "  desktop-windows       desktop client Windows .exe + NSIS installers (amd64+arm64, cross-built)"
	@echo "  bind-ios              gomobile xcframework → daemon/mobile/build/"
	@echo "  bind-android          gomobile aar → clients/android-*/app/libs/"
	@echo "  app-macos             native macOS app (xcodegen + xcodebuild)"
	@echo "  app-macos-unsigned    universal Release app, unsigned → dist/macos-unsigned/ (VERSION=x.y.z)"
	@echo "  dmg-macos             app-macos-unsigned + dmg-macos-package (VERSION=x.y.z)"
	@echo "  dmg-macos-package     Developer ID sign + dmg + notarize → dist/DiscoDrive-<v>-macos.dmg"
	@echo "  app-ios               iOS app (xcodegen + xcodebuild)"
	@echo "  ios-archive           App Store archive → dist/ios/DiscoDrive.xcarchive (VERSION=x.y.z [BUILD=n])"
	@echo "  ios-upload            send that archive to App Store Connect (TestFlight) via the Xcode account"
	@echo "  app-ios-fastsync      folder-sync iOS app   (⇒ bind-ios)"
	@echo "  app-android           full Android app      (⇒ bind-android)"
	@echo "  app-android-fastsync  folder-sync Android   (⇒ bind-android)"
	@echo "  all-go                headless daemon for host"
	@echo "  test | test-go | test-swift"
	@echo "  doctor                check required toolchains"
	@echo "  clean                 remove build artifacts"
	@echo ""
	@echo "Cross-build notes: desktop Linux needs Docker (see desktop-linux); desktop Windows"
	@echo "cross-builds from macOS; iOS/macOS apps need macOS+Xcode; Android needs SDK/NDK."

# ---------- DAEMON ----------
daemon:
	cd $(DAEMON_DIR) && CGO_ENABLED=$(DAEMON_CGO) GOOS=$(OS) GOARCH=$(ARCH) \
	  go build $(DAEMON_TAGS) -o ../$(OUT)/discodrive$(EXE) ./cmd/discodrive
	@echo "built → $(OUT)/discodrive$(EXE)"

# What a release ships: the headless daemon for Linux servers and NAS. Desktops and phones
# sync folders inside the apps.
daemon-all:
	$(MAKE) daemon OS=linux ARCH=amd64
	$(MAKE) daemon OS=linux ARCH=arm64

# ---------- DESKTOP ----------
DESKTOP_DIR := $(DAEMON_DIR)/cmd/discodrive-wails

# Linux build needs WebKitGTK; Wails cannot cross-compile from macOS and there is
# no official Wails image, so build inside Debian (see scripts/linux.Dockerfile).
# Defaults to the host's native arch; force amd64 with LINUX_ARCH=amd64 (emulated
# on Apple Silicon). Binary is exported to dist/linux/.
LINUX_ARCH ?=
desktop-linux:
	docker build -f scripts/linux.Dockerfile -o type=local,dest=$(DIST)/linux \
	  $(if $(LINUX_ARCH),--platform=linux/$(LINUX_ARCH),) .
	@echo "built → $(DIST)/linux/ (Linux binary)"

# Windows cross-builds cleanly from macOS: Wails v2's Go WebView2Loader is pure-Go
# (no CGO), so `wails build -platform windows/*` + NSIS (-nsis, needs makensis) work
# from here. Runtime (WebView2 render) still needs a Windows machine to verify.
WIN_ARCHES ?= amd64 arm64
desktop-windows:
	@mkdir -p $(DIST)/windows
	cd $(DESKTOP_DIR) && export PATH="$$(go env GOPATH)/bin:$$PATH" && set -e && \
	  for a in $(WIN_ARCHES); do \
	    wails build -platform windows/$$a -webview2 download -nsis; \
	    cp build/bin/DiscoDrive.exe "$(CURDIR)/$(DIST)/windows/DiscoDrive-$$a.exe"; \
	    cp build/bin/DiscoDrive-$$a-installer.exe "$(CURDIR)/$(DIST)/windows/"; \
	  done
	@echo "built → $(DIST)/windows/ (Windows .exe + NSIS installers, cross-built from macOS)"

# ---------- MOBILE BINDINGS ----------
bind-ios:
	cd $(DAEMON_DIR) && mkdir -p mobile/build && gomobile bind -target=ios -o mobile/build/Kfmobile.xcframework ./mobile
	@echo "built → daemon/mobile/build/Kfmobile.xcframework"

# -z max-page-size=16384: Android is moving to 16 KB memory pages, and a .so whose LOAD
# segments are 4 KB-aligned only runs in a compatibility mode there (the system says so in a
# dialog). Aligning at link time is the whole fix; it costs a little padding in the .so.
bind-android:
	cd $(DAEMON_DIR) && mkdir -p mobile/build && gomobile bind -target=android -androidapi 21 \
		-ldflags="-extldflags=-Wl,-z,max-page-size=16384" \
		-o mobile/build/kfmobile.aar ./mobile
	mkdir -p clients/android-discodrive/app/libs clients/android-fastsync/app/libs
	cp $(DAEMON_DIR)/mobile/build/kfmobile.aar clients/android-discodrive/app/libs/kfmobile.aar
	cp $(DAEMON_DIR)/mobile/build/kfmobile.aar clients/android-fastsync/app/libs/kfmobile.aar
	@echo "aar → clients/android-*/app/libs/"

# ---------- CLIENT APPS ----------
app-macos:
	cd clients/macos && xcodegen generate && \
	  xcodebuild -project DiscoDrive.xcodeproj -scheme DiscoDrive -configuration Release -derivedDataPath build build

# Release build of the native app with no signing at all, for scripts/package-macos-native.sh
# to sign. Split like this so CI builds (Go, SwiftPM) in a job without the signing key and
# signs in another that builds nothing; locally dmg-macos runs both, in this order.
# CFBundleVersion only ever increases: major*1000000 + minor*1000 + patch.
app-macos-unsigned:
	@echo "$(VERSION)" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$$' || { echo "VERSION=x.y.z is required" >&2; exit 1; }
	cd clients/macos && xcodegen generate && \
	  xcodebuild -project DiscoDrive.xcodeproj -scheme DiscoDrive -configuration Release \
	    -destination 'generic/platform=macOS' -derivedDataPath build build \
	    CODE_SIGNING_ALLOWED=NO ARCHS='arm64 x86_64' ONLY_ACTIVE_ARCH=NO \
	    MARKETING_VERSION=$(VERSION) \
	    CURRENT_PROJECT_VERSION=$$(echo "$(VERSION)" | awk -F'[.-]' '{print $$1*1000000 + $$2*1000 + $$3}')
	rm -rf $(DIST)/macos-unsigned && mkdir -p $(DIST)/macos-unsigned
	ditto clients/macos/build/Build/Products/Release/DiscoDrive.app $(DIST)/macos-unsigned/DiscoDrive.app
	@echo "built → $(DIST)/macos-unsigned/DiscoDrive.app"

dmg-macos-package:
	scripts/package-macos-native.sh $(VERSION)

dmg-macos:
	$(MAKE) app-macos-unsigned
	$(MAKE) dmg-macos-package

app-ios:
	cd clients/ios && xcodegen generate && \
	  xcodebuild -project DiscoDrive.xcodeproj -scheme DiscoDrive -sdk iphoneos -derivedDataPath build build

# App Store build of the iOS app. Signing is Xcode's automatic signing under the account
# logged into Xcode (team TQD2R45V4P); the upload re-signs it for distribution. BUILD
# (CFBundleVersion) defaults to the version formula used everywhere else; App Store
# Connect refuses a build number twice, so a second upload of one version needs BUILD=.
ios-archive:
	@echo "$(VERSION)" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$$' || { echo "VERSION=x.y.z is required" >&2; exit 1; }
	cd clients/ios && xcodegen generate
	rm -rf $(DIST)/ios/DiscoDrive.xcarchive
	xcodebuild -project clients/ios/DiscoDrive.xcodeproj -scheme DiscoDrive -configuration Release \
	  -destination 'generic/platform=iOS' -derivedDataPath clients/ios/build-device \
	  -archivePath $(DIST)/ios/DiscoDrive.xcarchive -allowProvisioningUpdates \
	  MARKETING_VERSION=$(VERSION) \
	  CURRENT_PROJECT_VERSION=$${BUILD:-$$(echo "$(VERSION)" | awk -F. '{print $$1*1000000 + $$2*1000 + $$3}')} archive
	@echo "archived → $(DIST)/ios/DiscoDrive.xcarchive"

ios-upload:
	@test -d $(DIST)/ios/DiscoDrive.xcarchive || { echo "no archive: run make ios-archive first" >&2; exit 1; }
	xcodebuild -exportArchive -archivePath $(DIST)/ios/DiscoDrive.xcarchive \
	  -exportOptionsPlist clients/ios/AppStoreUpload.plist -exportPath $(DIST)/ios/upload \
	  -allowProvisioningUpdates

app-ios-fastsync: bind-ios
	cd clients/ios-fastsync && xcodegen generate && \
	  xcodebuild -project DiscoDriveFastSync.xcodeproj -scheme DiscoDriveFastSync -sdk iphoneos -derivedDataPath build build

app-android: bind-android
	cd clients/android-discodrive && $(GRADLE_DD) assembleDebug

app-android-fastsync: bind-android
	cd clients/android-fastsync && $(GRADLE_FS) assembleDebug

# ---------- AGGREGATE / HOUSEKEEPING ----------
all-go: daemon

test: test-go test-swift

test-go:
	cd $(DAEMON_DIR) && go test ./...

test-swift:
	cd clients/DiscoKit && swift test

clean:
	rm -rf $(DIST)
	rm -rf $(DAEMON_DIR)/mobile/build
	rm -f clients/android-discodrive/app/libs/*.aar clients/android-fastsync/app/libs/*.aar
	rm -rf clients/macos/build clients/ios/build clients/ios-fastsync/build
	rm -rf clients/macos/*.xcodeproj clients/ios/*.xcodeproj clients/ios-fastsync/*.xcodeproj

# ---------- DOCTOR ----------
define _have
	@command -v $(1) >/dev/null 2>&1 && echo "  ok   $(1)$(2)" || echo "  MISS $(1)$(2)  — $(3)"
endef

doctor:
	@echo "Toolchain check (host: $(HOST_OS)/$(shell go env GOARCH)):"
	$(call _have,go,, required)
	$(call _have,gomobile, (mobile bindings), go install golang.org/x/mobile/cmd/gomobile@latest && gomobile init)
	$(call _have,xcodegen, (macOS/iOS apps), brew install xcodegen)
	$(call _have,xcodebuild, (macOS/iOS apps), install Xcode)
	$(call _have,gradle, (Android apps; or use Android Studio), brew install gradle)
	$(call _have,swift, (DiscoKit tests), install Xcode/Swift)
	$(call _have,wails, (desktop client toolchain), go install github.com/wailsapp/wails/v2/cmd/wails@latest)
	$(call _have,docker, (needed by desktop-linux), install Docker Desktop)
	$(call _have,makensis, (desktop Windows installers), brew install makensis)
	$(call _have,x86_64-w64-mingw32-gcc, (Windows daemon-tray cross-build), brew install mingw-w64)
	@echo "Env:"
ifeq ($(HOST_OS),darwin)
	@security find-identity -v -p codesigning 2>/dev/null | grep -q "Developer ID Application" \
	  && echo "  ok   Developer ID Application certificate (signed macOS releases)" \
	  || echo "  warn no Developer ID Application certificate — macOS builds are ad-hoc signed"
	@xcrun notarytool history --keychain-profile "$${NOTARY_PROFILE:-discodrive}" >/dev/null 2>&1 \
	  && echo "  ok   notarytool keychain profile '$${NOTARY_PROFILE:-discodrive}'" \
	  || echo "  warn no notarytool profile '$${NOTARY_PROFILE:-discodrive}' — see dev/ops/macos-signing.md"
endif
	@[ -n "$$ANDROID_NDK_HOME" ] && echo "  ok   ANDROID_NDK_HOME=$$ANDROID_NDK_HOME" || echo "  MISS ANDROID_NDK_HOME — set for Android bindings"
	@[ -n "$$ANDROID_HOME" ] && echo "  ok   ANDROID_HOME=$$ANDROID_HOME" || echo "  warn ANDROID_HOME unset"
