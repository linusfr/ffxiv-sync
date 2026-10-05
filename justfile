#!/usr/bin/env just --justfile

# Show all available commands
@default:
    just --list

# The four machines this has to run on.
targets := "linux/amd64 windows/amd64 darwin/arm64 darwin/amd64"

build version="dev":
    go build -trimpath -ldflags="-X main.version={{version}}" -o dist/ffsync ./cmd/ffsync
    go build -trimpath -o dist/ffsync-server ./cmd/ffsync-server

# Cross-compile the client for every machine in the house
release-binaries version="dev":
    #!/usr/bin/env bash
    set -euo pipefail
    mkdir -p dist
    for target in {{targets}}; do
      os="${target%/*}"; arch="${target#*/}"
      out="dist/ffsync-{{version}}-$os-$arch"
      [ "$os" = "windows" ] && out="$out.exe"
      GOOS="$os" GOARCH="$arch" go build -trimpath \
        -ldflags="-s -w -X main.version={{version}}" -o "$out" ./cmd/ffsync
      echo "$out"
    done

test:
    go test -race ./...

vet:
    go vet ./...

fmt:
    gofmt -w .

fmt-check:
    #!/usr/bin/env bash
    set -euo pipefail
    unformatted=$(gofmt -l .)
    if [ -n "$unformatted" ]; then echo "needs gofmt:"; echo "$unformatted"; exit 1; fi

image:
    docker build -t ffxiv-sync:dev .

# Everything CI runs
check: fmt-check vet test build
    prek run --all-files

# Install the git hooks
hooks:
    prek install --install-hooks
    prek install --hook-type commit-msg

clean:
    rm -rf dist
