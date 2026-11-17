# 1. Hand-write the Schoolbox API client

Date: 2026-11-17 · Status: accepted

## Decision

Write the Go client for the Schoolbox API by hand, modelled on real responses. Don't generate it from the vendor's OpenAPI spec ([alaress/schoolbox-api-docs](https://github.com/alaress/schoolbox-api-docs)).

## Why

- **The spec is documentation, not a contract.** It is hand-written in a separate repo, and its only CI publishes the docs site. Nothing checks it against the server.
- **Real responses don't match it.** Against the published 26.1.9 and 26.1.17 specs (the target instance runs 26.1.16):
  - the news feed fails validation in 496 places across 37 articles (relative links, null `viewedAt`);
  - every evidence feed item fails the spec's `oneOf`;
  - `/api/user` returns numbers where the spec says strings.
- **Generation needed fixes and still gave poor code.** oapi-codegen v2.8.2 and ogen v1.25.0 both fail on the full spec. They only worked on a 7-endpoint subset after 8 fixes to the spec. That produced 5.6k lines (oapi-codegen) or 36k+ (ogen), with untyped unions.
- **The surface is small.** A parent token can read 5 useful endpoints, about 400 lines of hand-written Go.

## Consequences

- Types cover only the fields we use. They are checked by tests on fixtures shaped like captured responses, not by the spec.
- Schema changes won't show up at generation time; they show up as decode failures or zero values. Re-run `scripts/probe.sh` after Schoolbox upgrades.
- The spec is still useful for finding endpoints and what they mean, and for the permission notes in its descriptions.
