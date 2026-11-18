# unboxed

Connect to Schoolbox with your AI agents via MCP. Shipped as a single static executable.

## Go version

This project uses **Go 1.27**, which was released after the AI knowledge cutoff. Do not rely on training data for Go stdlib or dependency APIs — always fetch current documentation via Context7 before using an unfamiliar API.

All JSON encoding and decoding uses Go 1.27's `encoding/json/v2` (and
`encoding/json/jsontext` where needed). Importing the v1 `encoding/json` package
is categorically forbidden; the repository-wide `depguard` rule enforces this
for production code, tests, and tools.

## Build and test

```
just verify    # fmt + build + lint + test (run before committing)
just build     # produces dist/unboxed
just test      # go test ./...
just snapshots # regenerate go-snaps golden files for review
just fmt       # gofmt -w .
just lint      # golangci-lint run ./...
just xbuild    # goreleaser snapshot build across the full release matrix
```

Tool versions (Go, golangci-lint, goreleaser, just) are pinned in `mise.toml`; run `mise install` to match CI. CI reads the same versions via `mise current`.

## Unit tests

- Use `github.com/stretchr/testify/assert` for independent checks and
  `github.com/stretchr/testify/require` for prerequisites after which the test
  cannot continue. **All assertions use Testify**: do not call `t.Error`,
  `t.Errorf`, `t.Fatal`, `t.Fatalf`, `t.Fail`, or `t.FailNow`, and do not build
  generic hand-written assertion helpers that duplicate Testify.
- Prefer the semantic Testify assertion (`NoError`, `ErrorIs`, `Equal`,
  `Contains`, `Len`, and so on) over asserting a precomputed boolean. Remember
  that `Equal` is type-sensitive; use `EqualValues` only when the production
  comparison intentionally permits equivalent numeric types.
- Use `require` only from the test goroutine. Collect worker-goroutine errors
  and assert them from the test goroutine.
- Every golden test uses `github.com/gkampitakis/go-snaps/snaps`; do not add
  ad-hoc `testdata/*.golden` readers or update flags. Use `MatchJSON` for JSON
  and `MatchSnapshot` for other values. Commit generated `__snapshots__` files,
  regenerate them with `just snapshots`, and review their diffs rather than
  editing them by hand.
- A package containing go-snaps tests must call `snaps.Clean` from its
  `TestMain` so obsolete snapshots fail the normal test run and can be removed
  during regeneration.

## Cross-platform rules

Linux, macOS and Windows on `amd64` and `arm64` are equal release targets. CI tests on Linux only, so nothing catches a macOS- or Windows-only failure before release: use `path/filepath` for paths, keep filenames legal on Windows, build-tag anything POSIX-only, and keep `CGO_ENABLED=0`.

## Project layout

`cmd/cli/` holds the `main` package, built as the `unboxed` binary. Application logic grows into packages alongside it; `main` stays a thin layer over them.

## Commits and PR titles

Use Conventional Commits for all commit messages and PR titles. `pr-title.yml` enforces the format on PRs (the squash-merge commit is taken from the PR title). **Commit types carry version-bump semantics**: release-please is wired up, so `feat:` drives a minor bump and `fix:` a patch bump, and a `!`/`BREAKING CHANGE` drives a major.

| Type | When to use |
|---|---|
| `feat: <description>` | new user-visible feature (minor bump) |
| `fix: <description>` | bug fix (patch bump) |
| `chore:`, `docs:`, `refactor:`, `test:`, `ci:` | maintenance, no release |

## Key conventions

- Service configuration is environment variables, not flags.
- `http.Server` always gets an explicit `ReadHeaderTimeout` and `IdleTimeout`.

## Releases

release-please opens a release PR from Conventional Commits on `main`. Merging it creates a draft release and pushes the `v*` tag; `release.yml` then runs the shared chinmina GoReleaser workflow, which builds the matrix, attests provenance, publishes the draft, and commits the Homebrew cask to `jamestelfer/homebrew-tap` and the Nix recipe to `jamestelfer/nixpkgs`. The workflows mint keyless tokens through octo-sts, against the `release-please-unboxed`, `release-unboxed` and `release-tap` trust policies in `jamestelfer/.github`. Repository configuration follows `docs/reference/repository-configuration.md` in `chinmina/.github`.

## Major dependencies

Use Context7 for up-to-date documentation — do not guess at APIs. Query the Context7 ID before using any unfamiliar or version-sensitive API (Go 1.27 and several of these post-date the knowledge cutoff). Every ID below has been resolved and confirmed to exist; do not invent one.

| Library / source | Context7 ID | Notes |
|---|---|---|
| `github.com/urfave/cli/v3` | `/urfave/cli` | **The CLI framework. All command line argument handling uses urfave/cli v3** — never `flag`, never a hand-rolled parser. Note v3 is `cli.Command`, not v2's `cli.App`, so most v2 answers do not transfer |
| GoReleaser | `/goreleaser/goreleaser` | Release/snapshot builds; ldflags inject `main.version`/`commit`/`date` |
| `github.com/stretchr/testify` | `/stretchr/testify` | All unit-test assertions; use `assert` for independent checks and `require` for prerequisites |
| `github.com/gkampitakis/go-snaps` | `/gkampitakis/go-snaps` | All golden/snapshot tests; JSON uses `snaps.MatchJSON` |

The Go stdlib is not on Context7: use upstream docs directly. Watch for Go 1.27 idioms the linter enforces, such as `errors.AsType[T](err)` over `errors.As(err, &target)`.

### CLI conventions

`run(args, stdout, stderr) int` stays a pure function of its arguments so it is
testable without a process boundary. urfave/cli's default `ExitErrHandler`
writes to a package global and calls `os.Exit`, so it is replaced with a no-op
and exit-code mapping is done in `run`. Exit codes: `0` success, `1` runtime
failure, `2` usage error.
