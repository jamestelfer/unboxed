# unboxed

Connect to Schoolbox with your AI agents via MCP.

`unboxed mcp` runs a read-only [Model Context Protocol](https://modelcontextprotocol.io/) server over stdio. It gives an AI agent a parent's view of Schoolbox: timetable, due work, marked work and results, and school news with its attachments.

## Usage

The server reads its configuration from the environment:

| Variable | Value |
| -------- | ----- |
| `SCHOOLBOX_URL` | Root URL of the Schoolbox site, for example `https://schoolbox.example.edu.au` |
| `SCHOOLBOX_TOKEN` | Schoolbox bearer token (a JWT) for the parent's account |

Treat the token as a password. It grants the same read access as the parent's login.

Add the server to your MCP client's configuration. Claude Desktop, Claude Code (`.mcp.json`) and most other clients use this format:

```json
{
  "mcpServers": {
    "schoolbox": {
      "command": "unboxed",
      "args": ["mcp"],
      "env": {
        "SCHOOLBOX_URL": "https://schoolbox.example.edu.au",
        "SCHOOLBOX_TOKEN": "<token>"
      }
    }
  }
}
```

Keep a configuration file that holds the token out of version control.

### Tools

Every tool is read-only.

| Tool | Returns |
| ---- | ------- |
| `list_students` | The children linked to the account, with the `student_id` the other tools take |
| `get_calendar` | Classes, events, excursions and due work between two dates. Defaults to the next 7 days |
| `get_due_work` | Homework and assessments due between two dates. Defaults to the next 14 days |
| `list_awaiting_feedback` | Past due work with no mark or feedback yet. Defaults to the last 14 days, ending yesterday |
| `list_marked_work` | Marked work with the mark, subject, teacher and written feedback. Defaults to the last 14 days |
| `grade_stats` | Mark statistics by subject and work type, with trend and grade tallies. Defaults to the current semester |
| `list_news_topics` | The school's news topics, with the slug `list_news` takes |
| `list_news` | News articles addressed to the parent, with their topics, optionally filtered by topic slug or name |
| `get_article` | One news article as plain text, with the names of its attachments |
| `get_attachment` | A file attached to a news article, up to 10 MB |

Date ranges for the calendar, due work and awaiting feedback are capped at 31 days.

`list_awaiting_feedback` cannot tell whether work was submitted. Schoolbox's calendar `completed` flag stays false even for marked work, so the tool relies on the absence of feedback instead.

`grade_stats` separates completion statuses such as "Submitted" from numeric marks. Schoolbox reports descriptor grades (for example "C") as fixed stand-in percentages, so these are flagged rather than averaged as genuine marks.

## Installation

Pre-built binaries for Linux, macOS, and Windows (amd64 and arm64) are published to [GitHub Releases](https://github.com/jamestelfer/unboxed/releases). Every artifact carries a build provenance attestation; see [Verifying releases](#verifying-releases).

<details>
<summary><strong>mise (recommended)</strong></summary>

[mise](https://mise.jdx.dev/) installs from GitHub Releases and verifies the artifact's checksum and build provenance attestation:

```sh
mise use -g github:jamestelfer/unboxed
```

</details>

<details>
<summary><strong>Install script</strong></summary>

Each release includes an installer that detects your platform and checks the download against the checksums embedded in the script:

```sh
curl -fsSL https://github.com/jamestelfer/unboxed/releases/latest/download/install.sh | sh
```

It installs to `~/.local/bin`. Pass `-b` for another directory. To pin a version, fetch the script from that release, so its embedded checksums match:

```sh
curl -fsSL https://github.com/jamestelfer/unboxed/releases/download/v1.2.3/install.sh \
  | sh -s -- -b /usr/local/bin v1.2.3
```

The script has its own attestation. A verified script holds the genuine checksums for its release and enforces them on the binary, so verifying the script covers the binary for that release:

```sh
curl -fsSL -O https://github.com/jamestelfer/unboxed/releases/latest/download/install.sh
gh attestation verify install.sh --repo jamestelfer/unboxed \
  --signer-workflow chinmina/.github/.github/workflows/goreleaser-release.yml
sh install.sh
```

</details>

<details>
<summary><strong>Homebrew</strong></summary>

```sh
brew install jamestelfer/tap/unboxed
```

</details>

<details>
<summary><strong>Nix</strong></summary>

```sh
nix profile install github:jamestelfer/nixpkgs#unboxed
```

</details>

<details>
<summary><strong>Manual download</strong></summary>

Download the archive for your platform, verify its provenance, and put the binary on your `PATH`:

```sh
OS=linux ARCH=amd64   # or darwin/windows, arm64
curl -fsSLO "https://github.com/jamestelfer/unboxed/releases/latest/download/unboxed_${OS}_${ARCH}.tar.gz"
gh attestation verify "unboxed_${OS}_${ARCH}.tar.gz" --repo jamestelfer/unboxed \
  --signer-workflow chinmina/.github/.github/workflows/goreleaser-release.yml
tar -xzf "unboxed_${OS}_${ARCH}.tar.gz" unboxed
install -m 0755 unboxed ~/.local/bin/
```

Windows archives are `.zip`.

</details>

<details>
<summary><strong>go install</strong></summary>

Builds from source report the module version, such as `v0.1.0`, instead of the release build metadata.

```sh
go install github.com/jamestelfer/unboxed/cmd/unboxed@latest
```

</details>

## Verifying releases

Release archives and `install.sh` carry a [build provenance attestation](https://docs.github.com/en/actions/security-for-github-actions/using-artifact-attestations/using-artifact-attestations-to-establish-provenance-for-builds) created with [Sigstore](https://www.sigstore.dev/) keyless signing. Each artifact is bound, by digest, to the source commit and the workflow run that built it. No long-lived signing key exists.

Verify a downloaded artifact with the [GitHub CLI](https://cli.github.com/) (2.49.0 or later):

```sh
gh attestation verify unboxed_linux_amd64.tar.gz --repo jamestelfer/unboxed \
  --signer-workflow chinmina/.github/.github/workflows/goreleaser-release.yml
```

`--repo` checks the source repository. `--signer-workflow` is required because the artifacts are signed by the shared release workflow in `chinmina/.github`, not by a workflow in this repository.

`checksums.txt` lists every archive. After downloading it alongside an archive, check with `sha256sum --check --ignore-missing checksums.txt`, or `shasum -a 256 --check --ignore-missing checksums.txt` on macOS.

Releases are immutable: once published, their assets cannot be changed and their tag cannot be moved. GitHub records a release attestation of the tag, commit, and every asset. Check a downloaded file against it with GitHub CLI 2.81.0 or later:

```sh
gh release verify-asset v1.2.3 unboxed_linux_amd64.tar.gz --repo jamestelfer/unboxed
```

## Development

Run `mise install` for the pinned toolchain, then `just verify` before committing. See `AGENTS.md` for conventions.

## Licence

Apache 2.0. See `LICENSE`.
