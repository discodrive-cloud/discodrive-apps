# discodrive-apps — unified build orchestrator.
# Run from the repo root. `make help` lists targets; `make doctor` checks toolchains.
# Override per-invocation: make daemon OS=windows ARCH=amd64 TRAY=1

DAEMON_DIR := daemon
DIST       := dist
HOST_OS    := $(shell go env GOOS)
OS   ?= $(HOST_OS)
ARCH ?= $(shell go env GOARCH)
TRAY ?= 0
# dmg version label; empty ⇒ fall back to the app's Info.plist (see package-macos-dmg.sh)
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

.PHONY: help daemon daemon-all daemon-tray-all bind-ios bind-android \
        app-macos app-ios app-ios-fastsync app-android app-android-fastsync \
        desktop app-desktop-macos dmg-desktop-macos desktop-linux desktop-windows \
        all-go test test-go test-swift doctor clean

help:
	@echo "discodrive-apps — build targets (override OS=, ARCH=, TRAY=1):"
	@echo "  daemon                headless sync daemon for OS/ARCH  (TRAY=1 ⇒ menubar/tray flavor)"
	@echo "  daemon-all            server daemon (no tray): darwin amd64+arm64, linux amd64, windows amd64"
	@echo "  daemon-tray-all       desktop daemon (tray): linux amd64+arm64, windows amd64 (+darwin on macOS)"
	@echo "  desktop               desktop client for host (darwin/universal on macOS)"
	@echo "  app-desktop-macos     universal DiscoDrive.app (ad-hoc signed)"
	@echo "  dmg-desktop-macos     package the desktop .app into dist/*.dmg  (VERSION=x.y.z names the file)"
	@echo "  desktop-linux         desktop client Linux build via Debian Docker image"
	@echo "  desktop-windows       desktop client Windows .exe + NSIS installers (amd64+arm64, cross-built)"
	@echo "  bind-ios              gomobile xcframework → daemon/mobile/build/"
	@echo "  bind-android          gomobile aar → clients/android-*/app/libs/"
	@echo "  app-macos             native macOS app (xcodegen + xcodebuild)"
	@echo "  app-ios               iOS app (xcodegen + xcodebuild)"
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

daemon-all:
	$(MAKE) daemon OS=darwin  ARCH=amd64
	$(MAKE) daemon OS=darwin  ARCH=arm64
	$(MAKE) daemon OS=linux   ARCH=amd64
	$(MAKE) daemon OS=windows ARCH=amd64

# Desktop flavor (tray menu + icon). Pure-Go targets build anywhere; the darwin
# tray needs cgo, so it is only attempted on a macOS host.
daemon-tray-all:
	$(MAKE) daemon OS=linux   ARCH=amd64 TRAY=1
	$(MAKE) daemon OS=linux   ARCH=arm64 TRAY=1
	$(MAKE) daemon OS=windows ARCH=amd64 TRAY=1
ifeq ($(HOST_OS),darwin)
	$(MAKE) daemon OS=darwin ARCH=amd64 TRAY=1
	$(MAKE) daemon OS=darwin ARCH=arm64 TRAY=1
endif

# ---------- DESKTOP ----------
DESKTOP_DIR := $(DAEMON_DIR)/cmd/discodrive-wails

desktop app-desktop-macos:
	cd $(DESKTOP_DIR) && export PATH="$$(go env GOPATH)/bin:$$PATH" && \
	  wails build -platform darwin/universal -clean
	@echo "built → $(DESKTOP_DIR)/build/bin/discodrive-wails.app"

dmg-desktop-macos: app-desktop-macos
	scripts/package-macos-dmg.sh $(VERSION)

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
	    cp build/bin/discodrive-wails-$$a-installer.exe "$(CURDIR)/$(DIST)/windows/"; \
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
	  xcodebuild -project DiscoDrive.xcodeproj -scheme DiscoDrive -derivedDataPath build build

app-ios:
	cd clients/ios && xcodegen generate && \
	  xcodebuild -project DiscoDrive.xcodeproj -scheme DiscoDrive -sdk iphoneos -derivedDataPath build build

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
	@[ -n "$$ANDROID_NDK_HOME" ] && echo "  ok   ANDROID_NDK_HOME=$$ANDROID_NDK_HOME" || echo "  MISS ANDROID_NDK_HOME — set for Android bindings"
	@[ -n "$$ANDROID_HOME" ] && echo "  ok   ANDROID_HOME=$$ANDROID_HOME" || echo "  warn ANDROID_HOME unset"
