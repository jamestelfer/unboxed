# unboxed

Connect to Schoolbox with your AI agents via MCP.

## Installation

Pre-built binaries for Linux, macOS, and Windows (amd64 and arm64) are published to
[GitHub Releases](https://github.com/jamestelfer/unboxed/releases). Every archive
carries a build provenance attestation.

Verify a downloaded archive with the [GitHub CLI](https://cli.github.com/):

```sh
gh attestation verify unboxed_linux_amd64.tar.gz --repo jamestelfer/unboxed \
  --signer-workflow chinmina/.github/.github/workflows/goreleaser-release.yml
```

Install with any of:

```sh
mise use -g github:jamestelfer/unboxed
brew install jamestelfer/tap/unboxed
nix profile install github:jamestelfer/nixpkgs#unboxed
go install github.com/jamestelfer/unboxed/cmd/cli@latest
```

## Development

Run `mise install` for the pinned toolchain, then `just verify` before committing.
See `AGENTS.md` for conventions.

## Licence

Apache 2.0. See `LICENSE`.
