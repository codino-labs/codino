# Host side. The plugin's bin/ holds the launcher (bin/codino, committed) and
# one binary per OS and CPU, named codino-<GOOS>-<GOARCH>[.exe], which the
# launcher picks from. All are pure Go (CGO_ENABLED=0), cross-compiled here.
#
#   make plugin       the binary for this machine only (quick local testing:
#                     claude --plugin-dir plugin)
#   make plugin-all   every platform the plugin ships for
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
# -trimpath and -s -w keep the six binaries the plugin carries small.
GOFLAGS_BUILD := -trimpath -ldflags='-s -w'

.PHONY: plugin plugin-all check clean

plugin:
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build $(GOFLAGS_BUILD) \
		-o plugin/bin/codino-$(GOOS)-$(GOARCH)$(if $(filter windows,$(GOOS)),.exe) ./cmd/codino

plugin-all:
	@for p in $(PLATFORMS); do \
		$(MAKE) --no-print-directory plugin GOOS=$${p%/*} GOARCH=$${p#*/} || exit 1; \
	done

# The Go done-check from AGENTS.md.
check:
	test -z "$$(gofmt -l .)" && go vet ./... && go test -race ./...

clean:
	rm -f plugin/bin/codino-*
