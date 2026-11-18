# Plan: `get_assessment` tool

A read-only MCP tool that returns one assessment as seen by a parent: task details, description, task files, grading, rubric and submission history. The source is the HTML page at `/learning/assessments/{assessmentId}/{studentId}`, which has no JSON alternative.

## Findings from real pages

Nine pages were captured on Schoolbox 26.1.17 (raw copies in `probe-out/assess/`, gitignored). Together they cover a raw mark, a percentage, a descriptor grade, "Not Submitted", "Submitted", "Resubmission required" with a rubric grid, and two upcoming tasks.

- The bearer token works for this page, so no session-cookie exchange is needed. With no credentials, the response is a `302` to `/login?page=…`. A student who is not the parent's child gets a `401` HTML page. An unknown assessment gets a `404` HTML page.
- Pages are 42–69 KB. All task content sits inside `div#content`, which is 1–32 KB of each page.
- The visible date text is relative ("Friday", "last Monday"), but every due date also carries a `<time datetime="…+11:00">` with an RFC 3339 value.
- The rubric is not HTML. It is a Vue component, `<rubric-readonly :rubric-marked-grids-payload="[…]" :assessment-mark-scheme="{…}">`, whose attributes hold entity-encoded JSON. The payload is `[]` when there is no rubric. The `hide` class on `.rubric-wrap` only collapses the UI; the data is present either way.
- The Grading block and the Submission History repeat the same feedback. The history adds the student's submissions and their files.
- The Grading card verb varies: "Feedback by …" or "Resubmission required by …". An entry with no mark omits `[data-test="assessment-feedback-mark"]`.
- Feedback timestamps are plain text only (`Nov 18, 2026 12:36pm`), in the school's local time.
- Pages link three kinds of file:
  - **Task files** are attached by the teacher and link to `/storage/fetch.php?hash=…&name=…`, the store `Client.Download` already handles. The page shows their size as text, such as `476.79 KB`. Three of the nine pages have one each: two Word documents of 106 KB and 477 KB, and a 940 B CSV.
  - **Student submissions** link to `/learning/assessments/{aid}/{sid}/responses/{rid}/files/{fid}/download`. The page shows no size for them.
  - **Annotated feedback** links to `/learning/kami/{id}`, a Kami viewer page rather than a file.
- The one rubric seen is about 4.9 KB of JSON: 1 criterion, 7 indicators, 5 levels and 35 descriptors, with about 1.6 KB of descriptor text. Level names and descriptors contain HTML (`<p>…</p>`). As plain text the whole rubric is roughly 2 KB.
- `student_id` must be the right child. The wrong child or the parent's own ID gets a `401`, and `0` gets a `404`. Leaving it off (`/learning/assessments/{aid}`) gets a `303` to the enrolled child's URL. The tool does not use that redirect, for the reasons in the tool contract.
- The task description is `article.content`. The `id="ir-content"` attribute appears on only some pages.
- Error pages share the chrome but have no `ul.meta` and show `h2.subheader` "Possible Reasons" or "Your Options".

## Parsing approach: recommendation

**Use `html.Parse` on the whole page, then run cascadia selectors scoped to `#content`.** Do not pre-scan with the tokenizer.

Measured on the nine captured pages (Raspberry Pi 5, arm64, Go 1.27.4), in a scratch benchmark kept outside the repository:

| Approach | Time per page | Allocated per page | Allocs per page |
|---|---|---|---|
| `html.Parse` whole page + cascadia | ~1.1 ms | ~227 KB | ~4,500 |
| Tokenizer to `#content`, `html.ParseFragment`, cascadia | ~0.64 ms | ~96 KB | ~2,300 |

Both approaches extracted identical fields on every page. The fragment approach saves about 0.5 ms and 130 KB per call. The HTTP round trip is about 330 ms, so the saving is under 0.2 % of the tool's latency, and memory is freed after each call.

The cost of the fragment approach is about 40 extra lines of depth-counting code. That code breaks on unclosed or misnested `div`s, which the HTML5 tree builder repairs for free. It also needs a synthetic `<body>` context node and a document root before selectors work. A full parse gives the same tree the browser builds, so selectors copied from browser dev tools keep working. Revisit this only if profiling shows parsing matters, for example in a batch tool that fetches dozens of pages.

Keep regex out of extraction. The only text processing is trimming whitespace, splitting "Feedback by Name" on the first ` by `, and `time.ParseInLocation` for feedback timestamps.

### cascadia API (v1.3.5, checked with `go doc`; not indexed by Context7)

- `cascadia.MustCompile(sel) Selector` at package level. Then `sel.MatchFirst(n)`, `sel.MatchAll(n)`, or `cascadia.Query(n, m)` and `cascadia.QueryAll(n, m)`.
- `:has()`, `:contains()`, `:not()` and attribute selectors are supported.
- Attribute values arrive already entity-decoded from `x/net/html`, so the rubric attribute goes straight to `json.Unmarshal` (v2).

## Field map

Stability: **High** means the selector uses `data-test` hooks, `<time datetime>`, or Vue component props, which Schoolbox's own tests or front end depend on. **Medium** means structural Foundation classes that are stable but cosmetic. **Low** means text or icon names.

| Field | Selector (scoped to `#content`) | Stability |
|---|---|---|
| Title | `h1` (first) | Medium |
| Subject (class name) | `ul.breadcrumb li:nth-child(2) a` | Medium |
| Due | `ul.meta time[datetime]` (first) → `time.Parse(time.RFC3339, …)` | High |
| Work type | `ul.meta li:has(sbx-icon[name="due-work"]), ul.meta li:has(sbx-icon[name="task"])`, text | Low; the icon varies by type |
| Submission status | `ul.meta li:has(sbx-icon[name="upload"])`, text; absent on upcoming homework | Low |
| Description (HTML) | `article.content`, inner HTML via `html.Render` of children | Medium |
| Task files | `ul.grid .card.attachment a[href^="/storage/fetch.php"]`: `title` = name, `href` = link, `p.meta` = size text, `img[src^="/ajax/mime/"]` = MIME type | Medium |
| Grading entries | `[data-test="assessment-feedback-summary"] ul.reply > li` | High |
| · author and action | `.card p` (first text node): split "Feedback by X" / "Resubmission required by X" | Low |
| · timestamp | `.card p.meta` → `ParseInLocation("Jan 2, 2006 3:04pm", …, loc)` | Low |
| · mark | `[data-test="assessment-feedback-mark"] span`; optional | High |
| · comment (HTML) | `blockquote` inner HTML; `article span.meta` "Comment not provided" means empty | Medium |
| · annotated feedback files | `ul.information-list .card.attachment a` inside the entry: `p` = label, `href` = link | Medium |
| · Kami annotated feedback | `a[href^="/learning/kami/"]` inside the entry: label "Annotated Feedback"; link only, not a file | Low |
| Rubric | `[data-test="assessment-feedback-summary"] rubric-readonly` attr `:rubric-marked-grids-payload` → JSON | High (shape may change) |
| Mark scheme | same element, attr `:assessment-mark-scheme` → JSON (`mode` raw/letter, `conf`) | High (shape may change) |
| Submission history | `[data-test="assessment-submission-history"]` sections: `ul.reply > li` are feedback entries, `ul.threaded .card p` "Submission by X" are submissions | High root, Low internals |
| · submission files | `a[href*="/responses/"][href$="/download"]`: `title` = name, `href` = link; no size on the page | Medium |

Centralise every selector in one `var (…)` block in `internal/schoolbox/assessment.go`, each with a comment that names the Schoolbox version where it was observed. This follows the convention in `calendar.go`.

## Failure behaviour

| Condition | Detection | Tool result |
|---|---|---|
| `student_id` not a linked child | `resolveStudent` | Existing error listing valid students; no request sent |
| Token rejected or expired | `3xx` with `Location` path `/login` | `ErrLoginRequired`: "Schoolbox rejected the token; it may have expired" |
| Login page served as `200` | No `ul.meta` and a `form[action*="login"]` or `input[type=password]` | Same as above |
| `401`/`403` | Existing `APIError.Is(ErrPermission)` | "assessment A is not visible for student S" |
| `404` | `ErrNotFound` | "no assessment A for student S" |
| `200` but no `#content h1` | Selector miss | `ErrUnexpectedPage` naming the missing anchor, so drift after an upgrade is obvious |
| Unmarked or upcoming work | No grading block | `grading` omitted; history may hold only submissions |
| Entry without a mark | No mark element | `mark` empty, `status` from grades.Parse empty |
| No rubric | Payload `[]` or element missing | `rubric` omitted |
| Collapsed rubric (`hide`) | Ignored | Parsed normally |
| Rubric JSON fails to decode | `json.Unmarshal` error | Drop the rubric, add `warnings: ["rubric could not be read"]`, keep the rest |

`send` currently turns any non-200 into `APIError`, including `302`. Add a 3xx-to-login check in `send` so every HTML endpoint benefits. `CurrentUserID` gains the same diagnosis for free.

Cap the body at 4 MB with `io.LimitReader`, matching `CurrentUserID`.

## Tool contract

Input:

```go
type assessmentArgs struct {
	StudentID    int `json:"student_id,omitempty" jsonschema:"student id from list_students; may be omitted when the parent has one child"`
	AssessmentID int `json:"assessment_id" jsonschema:"assessment id from list_marked_work or get_due_work"`
}
```

`student_id` is required, as in every other tool. It goes through `resolveStudent`, which allows leaving it out only when the parent has one child. The tool always fetches `/learning/assessments/{aid}/{sid}` directly. It does not use the `303` from the bare assessment URL to find the child, for three reasons:

- Callers already have the student ID. Assessment IDs come from `list_marked_work` and `get_due_work`, which are per-student.
- The redirect costs a second round trip and needs its own redirect handling, because the client turns redirects off.
- It is unknown which child the redirect picks when siblings share an assessment.

Output (JSON field names are final; omit empty fields with `omitempty`):

```go
type assessmentOut struct {
	AssessmentID int        `json:"assessmentId"`
	Student      string     `json:"student"`
	Title        string     `json:"title"`
	Subject      string     `json:"subject,omitempty"`
	WorkType     string     `json:"workType,omitempty"`
	Due          string     `json:"due,omitempty"`        // RFC 3339 in school time
	Submissions  string     `json:"submissions,omitempty"` // e.g. "This assessment no longer accepts submissions."
	Description  string     `json:"description,omitempty"` // htmlToText
	TaskFiles    []FileRef  `json:"taskFiles,omitempty"`
	Grading      []Feedback `json:"grading,omitempty"`
	MarkScheme   *MarkScheme `json:"markScheme,omitempty"`
	Rubric       []Rubric   `json:"rubric,omitempty"`
	History      []HistoryEntry `json:"history,omitempty"`
	Link         string     `json:"link"`
	Warnings     []string   `json:"warnings,omitempty"`
}

type Feedback struct {
	Action  string   `json:"action"`  // "Feedback", "Resubmission required"
	Teacher string   `json:"teacher"`
	At      string   `json:"at,omitempty"`
	Mark    string   `json:"mark,omitempty"`
	Percent *float64 `json:"percent,omitempty"` // grades.Parse, as in list_marked_work
	Grade   string   `json:"grade,omitempty"`
	Status  string   `json:"status,omitempty"`
	Comment string   `json:"comment,omitempty"` // htmlToText
	Files   []FileRef `json:"files,omitempty"`
}

type FileRef struct {
	Name string `json:"name"`
	// task, submission, annotation or kami.
	Kind string `json:"kind"`
	// Size as the page shows it ("476.79 KB"); empty for submissions and Kami links.
	Size string `json:"size,omitempty"`
	// Absolute URL for direct download in a browser signed in to Schoolbox.
	URL string `json:"url"`
	// False for Kami links, which are viewer pages rather than files.
	Retrievable bool `json:"retrievable"`
}

type Rubric struct {
	Title    string      `json:"title"`
	Levels   []string    `json:"levels"`
	Criteria []Criterion `json:"criteria"` // capability → indicators → every descriptor, with the selected one flagged
	Mark     string      `json:"mark,omitempty"`
}
```

Layering follows the existing split:

- `internal/schoolbox` returns `AssessmentPage` with raw HTML strings (`DescriptionHTML`, `CommentHTML`), as `DueWorkFeedback.Comment` and `Article.Body` already do.
- `internal/server` converts them with `htmlToText`. The HTML subtrees are serialised with `html.Render` so `htmlToText(string)` is reused unchanged.
- Marks go through `grades.Parse(mark, nil, nil)` so percent, grade and status match `list_marked_work`.
- Links pass through `s.link`, so every `url` is absolute.
- Level names and descriptor text go through `htmlToText`. The rubric returns every descriptor, not only the selected one, because the full text is only about 2 KB.

### Attachments

`get_assessment` returns file references with name, kind, size and URL, never file content.

- **Direct download.** Each `FileRef.url` is an absolute Schoolbox URL. It opens in a browser where the parent is signed in to Schoolbox. Without a session, Schoolbox redirects to its login page. That is fine: the URL is for the parent, not the server. The token never appears in it.
- **Resource links.** Besides the structured output, the tool returns one `mcp.ResourceLink` content block per retrievable file, with `URI`, `Name`, `MIMEType` (when the page's mime icon gives it) and `Size` (when known). Clients that render resource links can offer the download. Clients that do not support them ignore the extra blocks and still have `url` in the structured output. Check the go-sdk docs on how a handler returns both its own `Content` and structured output.
- **Retrieval through the MCP.** A new `get_assessment_file` tool takes `student_id`, `assessment_id` and `name`. It re-fetches the assessment page and matches `name` against the files listed there, the same allowlist rule as `get_attachment`, so the tool cannot be pointed at arbitrary links. It downloads through `Client.Download` (session cookie, 10 MB limit) and returns content the same way as `get_attachment`. Extract that content mapping into a shared helper instead of copying it. Kami links are not retrievable, and the tool says so.

## Phases

### Phase 0: dependencies and decision record

- Add `github.com/andybalholm/cascadia` (BSD-2-Clause; v1.3.5 requires only `golang.org/x/net`). `golang.org/x/net` v0.62.0 is already a direct requirement in `go.mod` and `go.sum`, used by `internal/server/text.go`, so cascadia adds one module and no transitive bump.
- Add `github.com/gkampitakis/go-snaps` (MIT). It is not yet in `go.mod`; this is the repository's first snapshot test. Check its current API with Context7 `/gkampitakis/go-snaps`.
- No `.golangci.yml` change: depguard denies only `encoding/json$`, and both modules use `x/net/html`, not JSON v1. Confirm go-snaps' transitive deps do not import `encoding/json` from repository code (depguard checks only this module's imports).
- Write `docs/decisions/0002-scrape-assessment-html.md`: no JSON endpoint, full parse over token pre-scan, the benchmark numbers, and the selector stability policy.

Acceptance: `go mod tidy` leaves both modules as direct requirements; licences recorded in the ADR. Verify: `just verify`.

### Phase 1: tracer bullet (title and due date, end to end)

- `Client.Assessment(ctx, assessmentID, studentID) (AssessmentPage, error)`: `send(..., "text/html")`, then `html.Parse` and selectors for `h1` and `time[datetime]`.
- Register `get_assessment` in `server.New`, with `resolveStudent` and `readOnly` annotations.
- One anonymised full-page fixture (the raw-mark page), served by the existing `fixtures` Doer.
- `internal/schoolbox/assessment_test.go` with `TestMain` calling `snaps.Clean`, plus `snaps.MatchJSON` of the extracted struct.

Acceptance: the tool returns title, due and link for the fixture; an unlinked `student_id` errors without a request. Verify: `just snapshots` reviewed, then `just verify`.

### Phase 2: failure modes

- `send` maps 3xx to `/login` into `ErrLoginRequired`. `Assessment` detects `200` login pages and pages with no anchor.
- Fixtures: trimmed `401` and `404` pages, plus a synthetic login page. Use `fixtures.raw` for the `302`.

Acceptance: every row in the failure table has a test asserting `ErrorIs` or the tool error text. Verify: `just verify`.

### Phase 3: task details

Subject, work type, submission status, description, task files.

Fixtures (trimmed to `#content`): an upcoming task with a description and no files, and an upcoming task with a file and no description.

Acceptance: snapshots show the fields present or absent as the pages are. Verify: `just verify`.

### Phase 4: grading

Grading entries with action, teacher, local timestamp, mark run through `grades.Parse`, comment and annotated files.

Fixtures: percent, descriptor grade, "Not Submitted", "Resubmission required" with no mark, and "Comment not provided".

Acceptance: the percent, grade and status for each fixture match what `list_marked_work` reports for the same feed item. Verify: `just verify`.

### Phase 5: rubric and mark scheme

- Decode both Vue props with `encoding/json/v2` into internal wire types. Map `markedGrid` descriptor selections onto criteria.
- Fixtures: the rubric grid page (null selections), an empty payload, and a hand-edited copy with one `descriptorId` selected.

Acceptance: an empty payload omits `rubric`; undecodable JSON yields a warning, not an error. Verify: `just verify`.

### Phase 6: submission history

Feedback and submission entries, in page order, with submission file names.

Fixture: the "Submitted" class-work page with a student file.

Acceptance: the history snapshot lists the submission and the feedback; duplicates of `grading` are not collapsed. Verify: `just verify`.

### Phase 7: attachment retrieval and links

- Return `mcp.ResourceLink` blocks from `get_assessment` for retrievable files.
- Add `get_assessment_file` and the content helper shared with `get_attachment`.
- Confirm against a live page that `Client.Download` fetches a submission file (`/responses/…/download`), not just `/storage/fetch.php`.

Acceptance:
- Asking for a name the page does not list errors with the list of valid names.
- Asking for a Kami link errors with a clear message.
- A fixture download returns a resource with the right MIME type.
- `get_attachment` behaviour is unchanged, and its existing tests pass.

Verify: `just verify`.

### Phase 8: discoverability

- Add `assessmentId` to due-work entries from `get_calendar` and `get_due_work`, parsed from `Event.Path` (`/learning/assessments/{aid}/{sid}?returnUrl=…`).
- Update the tool descriptions and README.
- Extend `TestLiveEndpointsDecode` in `live_test.go` (skipped without `SCHOOLBOX_TOKEN`) to fetch one real assessment and assert a non-empty title.

Acceptance: a due-work item's `assessmentId` round-trips into `get_assessment`. Verify: `just verify`, then the live test with `direnv exec . go test ./internal/schoolbox -run TestLiveEndpointsDecode`.

## Fixture policy

- Keep raw captures only in `probe-out/` (gitignored).
- Committed fixtures go in `internal/schoolbox/testdata/assessment_*.html`, with Windows-legal names.
- Keep the one Phase 1 full page whole, to prove the chrome outside `#content` does not confuse selectors. Trim the rest to `<html><body><div id="content">…</div></body></html>`.
- Anonymise before committing:
  - Replace student, teacher and school names, user, portrait, assessment, response, file and rubric IDs, storage hashes, and analytics IDs.
  - Replace the hostname and app-store links.
  - Rewrite comment text to neutral prose of similar structure.
  - Make the replacements consistent, so one teacher's name and ID map to the same substitute across files.
- Check each committed fixture with a grep for the real surnames, the school name and the real hostname before committing.

## TODO

- Render the rubric as a Markdown table (indicators as rows, levels as columns, the selected descriptor marked) in addition to the structured `rubric` field. This is deferred until a marked rubric has been captured.

## Open questions

1. No captured page has a rubric with selections or a non-null `humanReadableMark`. The mapping is inferred from field names until a marked rubric is found.
2. It is unverified whether submission-file links download with the session cookie the way `/storage/fetch.php` links do (Phase 7 checks this).
3. It is unverified whether MCP clients in use render `ResourceLink` blocks. The structured `url` field covers clients that do not.

## Replan triggers

- Schoolbox exposes JSON for this page, for example via the `feedback` route working for parents. If so, drop scraping and use `get[T]`.
- A Schoolbox upgrade breaks any High-stability selector. That signals a template rewrite: re-capture the pages and revisit the field map.
- A marked rubric's payload shape differs from the null-selection grid.
- Pages grow past about 500 KB, or a batch tool fetches many pages per call. Re-run the benchmark, and reconsider the tokenizer pre-scan.
- go-snaps or cascadia pull in a dependency that depguard or the licence policy rejects.
