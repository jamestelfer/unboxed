# Run all checks before committing
verify: fmt build lint test

# Format all Go source files
fmt:
    gofmt -w .

# Run all tests
test *args:
    go test ./... {{args}}

# Regenerate go-snaps golden files; always review and commit the diff
snapshots *args:
    UPDATE_SNAPS=true go test ./... {{args}}

# Build the binary
build *args:
    #!/usr/bin/env bash
    set -euo pipefail
    mkdir -p dist
    env CGO_ENABLED=0 go build -trimpath -o dist/unboxed {{args}} ./cmd/unboxed

# Run linter
lint:
    golangci-lint run ./...

# Cross-platform snapshot build for every release matrix target (via goreleaser)
xbuild *args:
    goreleaser build --snapshot --clean {{args}}
