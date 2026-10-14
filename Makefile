# Host side. `make plugin` puts the codino binary into the Claude Code plugin
# for local testing (claude --plugin-dir plugin); per-OS packaging is #17.
GOOS ?= linux
GOARCH ?= $(shell go env GOARCH)

.PHONY: plugin check clean

plugin:
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -o plugin/bin/codino ./cmd/codino

# The Go done-check from AGENTS.md.
check:
	test -z "$$(gofmt -l .)" && go vet ./... && go test -race ./...

clean:
	rm -rf plugin/bin
