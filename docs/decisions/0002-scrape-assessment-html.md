# 2. Scrape the assessment page HTML

Date: 2026-11-19 · Status: accepted

## Decision

Read assessment details by parsing the HTML page at `/learning/assessments/{assessmentId}/{studentId}`. Parse the whole page with `golang.org/x/net/html` and query it with [cascadia](https://github.com/andybalholm/cascadia) selectors scoped to `#content`.

## Why

- **No JSON alternative.** A parent token cannot read any JSON endpoint that returns the task description, task files, grading, rubric or submission history. The page accepts the bearer token, so no session exchange is needed.
- **A full parse beats a tokenizer pre-scan.** Measured on nine captured pages (Raspberry Pi 5, arm64, Go 1.27.4):

  | Approach | Time per page | Allocated per page | Allocs per page |
  |---|---|---|---|
  | `html.Parse` whole page + cascadia | ~1.1 ms | ~227 KB | ~4,500 |
  | Tokenizer to `#content`, `html.ParseFragment`, cascadia | ~0.64 ms | ~96 KB | ~2,300 |

  Both extracted identical fields. The saving is under 0.2 % of the ~330 ms round trip. The pre-scan costs about 40 lines of depth-counting code that breaks on misnested `div`s, which the HTML5 tree builder repairs. A full parse builds the same tree as the browser, so selectors copied from browser dev tools keep working.
- **Regex stays out of extraction.** Text handling is limited to trimming whitespace, splitting bylines on ` by `, and parsing timestamps.

## Selector stability policy

Every selector lives in one `var` block in `internal/schoolbox/assessment.go`, noting the Schoolbox version it was observed on (26.1.17).

- **High**: `data-test` hooks, `<time datetime>` and Vue component props (`rubric-readonly`). Schoolbox's own tests and front end depend on them.
- **Medium**: Foundation structural classes (`ul.meta`, `article.content`, `.card.attachment`). Stable but cosmetic.
- **Low**: text and icon names (the byline verb, `sbx-icon[name]`).

A page that lacks `#content`, `ul.meta` or `h1` fails with `ErrUnexpectedPage`, naming the missing anchor, so template drift after an upgrade is obvious. A rubric or mark scheme that fails to decode becomes a warning; the rest of the page is still returned.

## Dependencies

| Module | Licence | Use |
|---|---|---|
| `github.com/andybalholm/cascadia` v1.3.5 | BSD-2-Clause | CSS selectors over `x/net/html`; requires only `golang.org/x/net`, already a direct dependency |
| `github.com/gkampitakis/go-snaps` v0.5.25 | MIT | Snapshot tests of the scraped pages (test-only) |

## Consequences

- A Schoolbox template change can break scraping without warning. Re-capture pages after upgrades and re-run `just snapshots`; a change to any High-stability selector signals a template rewrite.
- Committed fixtures in `internal/schoolbox/testdata/assessment_*.html` are anonymised captures. Raw captures stay in the gitignored `probe-out/`.
- If Schoolbox exposes JSON for this page, replace the scraper with `get[T]`.
- Re-run the parse benchmark if pages grow past about 500 KB or a tool fetches many assessment pages per call. At that scale the tokenizer pre-scan's saving may matter.
