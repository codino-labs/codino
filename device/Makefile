# Thin wrapper around CMake so the commands are easy to remember.
BUILD := build

.PHONY: all test update-goldens virtual-device web clean

all: $(BUILD)/CMakeCache.txt
	cmake --build $(BUILD) -j

$(BUILD)/CMakeCache.txt:
	cmake -S . -B $(BUILD) -DCMAKE_BUILD_TYPE=Debug

test: all
	ctest --test-dir $(BUILD) --output-on-failure

# Regenerates the Theme snapshot golden frames (test/golden/). Run it only
# when a change is meant to change how a built-in Theme looks, and review the
# diff before committing it.
update-goldens: all
	UPDATE_GOLDENS=1 $(BUILD)/device_core_test -tc="snapshot:*"

virtual-device: all

# --- Browser Virtual Device ------------------------------------------------
# The Device core built for WebAssembly, for the web UI's live preview
# (`codino ui`). The .wasm is committed, with the hashes of the sources it was
# built from, and embedded in the codino binary, so building the Host needs
# no C++ toolchain. Run `make -C device web` after changing the Device core:
# a Go test (internal/webui) fails while the committed build is stale.
#
# It fetches a pinned wasi-sdk (clang + libc++ for wasm32-wasi, about 125 MB)
# once into $(TOOLCHAINS), in user space, checking its SHA-256.
WASI_SDK_VERSION := 27
WEB_OUT := ../internal/webui/assets
TOOLCHAINS ?= $(or $(XDG_CACHE_HOME),$(HOME)/.cache)/codino-toolchain
WASI_OS := $(subst Linux,linux,$(subst Darwin,macos,$(shell uname -s)))
WASI_ARCH := $(subst aarch64,arm64,$(shell uname -m))
WASI_SDK_NAME := wasi-sdk-$(WASI_SDK_VERSION).0-$(WASI_ARCH)-$(WASI_OS)
WASI_SDK := $(TOOLCHAINS)/$(WASI_SDK_NAME)
# From the release's asset digests.
WASI_SDK_SHA256_x86_64-linux := b7d4d944c88503e4f21d84af07ac293e3440b1b6210bfd7fe78e0afd92c23bc2
WASI_SDK_SHA256_arm64-linux := 4cf4c553c4640e63e780442146f87d83fdff5737f988c06a6e3b2f0228e37665
WASI_SDK_SHA256_x86_64-macos := 163dfd47f989b1a682744c1ae1f0e09a83ff5c4bbac9dcd8546909ab54cda5a1
WASI_SDK_SHA256_arm64-macos := 055c3dc2766772c38e71a05d353e35c322c7b2c6458a36a26a836f9808a550f8
SHA256SUM := $(shell command -v sha256sum >/dev/null 2>&1 && echo sha256sum || echo shasum -a 256)
# Every file the build reads, sorted: their hashes go next to the .wasm.
WEB_SOURCES := $(sort $(wildcard core/*.h core/*.cpp)) third_party/ArduinoJson.h virtual/browser/main.cpp
WEB_FLAGS := --target=wasm32-wasip1 -std=c++17 -Os -fno-exceptions -fno-rtti -mexec-model=reactor \
	-Wall -Wextra -Wpedantic -Werror -Icore -isystem third_party -Wl,--strip-all

web: $(WASI_SDK)/bin/clang++
	$(WASI_SDK)/bin/clang++ --sysroot=$(WASI_SDK)/share/wasi-sysroot $(WEB_FLAGS) \
		$(filter %.cpp,$(WEB_SOURCES)) -o $(WEB_OUT)/device.wasm
	$(SHA256SUM) $(WEB_SOURCES) > $(WEB_OUT)/device.wasm.sources
	@if command -v node >/dev/null 2>&1; then node virtual/browser/check.mjs $(WEB_OUT)/device.wasm ../protocol/fixtures; \
	else echo "node not found: skipping the check of device.wasm against the protocol fixtures"; fi

$(WASI_SDK)/bin/clang++:
	@test -n "$(WASI_SDK_SHA256_$(WASI_ARCH)-$(WASI_OS))" || { echo "no pinned wasi-sdk for $(WASI_ARCH)-$(WASI_OS)"; exit 1; }
	mkdir -p $(TOOLCHAINS)
	curl -fL -o $(WASI_SDK).tar.gz \
		https://github.com/WebAssembly/wasi-sdk/releases/download/wasi-sdk-$(WASI_SDK_VERSION)/$(WASI_SDK_NAME).tar.gz
	echo "$(WASI_SDK_SHA256_$(WASI_ARCH)-$(WASI_OS))  $(WASI_SDK).tar.gz" | $(SHA256SUM) -c -
	tar -xzf $(WASI_SDK).tar.gz -C $(TOOLCHAINS)
	rm $(WASI_SDK).tar.gz

clean:
	rm -rf $(BUILD)
