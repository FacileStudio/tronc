# Changelog

All notable changes to `tronc` are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow semver —
while on `v0`, a breaking change bumps the minor.

## [Unreleased]

## [0.1.0] — 2026-08-05

First release. Extracted from the twelve Go APIs in the Facile suite, reconciling the
variants each had drifted into.

### Added

- `errors` — the `{"error":{"code","message"}}` envelope with nine codes and one
  code→status map.
- `httpjson` — `DecodeJSON` (1 MB cap, unknown fields rejected, single object enforced),
  `WriteJSON`, `WriteError`.
- `logger` — slog JSON on stdout, level parsing, and a `Wrap` seam so log shipping stays
  out of this module's dependency graph.
- `middleware` — `RequestLogger`, `CORS`, `Recoverer`.
- `httpx` — `NewRouter`, the standard chi chain.
- `health` — `Live`, `Ready`, `DB`, and `Mount` at both `/` and `/api`.
- `healthcheck` — `Handle` and `Probe`, so a distroless container can probe itself.
- `env` — `LoadCore` plus `String`, `Required`, `Int`, `Bool`, `Duration`, `List`.

### Fixed

Relative to the copies this replaces:

- `CORS` panics at construction when `*` is allowed alongside
  `Access-Control-Allow-Credentials: true`, which let any site read authenticated
  responses. Two apps shipped that pair.
- An empty allowed-origin list denies every cross-origin caller instead of falling back to
  localhost defaults.
- `Recoverer` runs directly under `RequestID` rather than innermost, so a panic raised in
  CORS or in the request logger is caught rather than escaping to `net/http`.
- A recovered panic answers with the error envelope instead of chi's bare text 500.
- Query-string redaction covers eleven credential-bearing parameter names in every app,
  rather than `token` in one of them; an unparsable query is reported as `[unparsable]`
  instead of being logged raw.
- `resource_exhausted` maps to 413 and `rate_limited` to 429. One app mapped
  `resource_exhausted` to 429, so its size and rate failures were indistinguishable.
- The logging response writer implements `Flush` and `Unwrap`, so streaming responses and
  `http.ResponseController` keep working through the middleware.

### Notes

- Requires Go 1.24. Mycelium runs 1.26.1 and Plume and Casier run 1.25.0, but 1.24.0 is the
  floor across the suite.
- The only dependency is `github.com/go-chi/chi/v5`.

[Unreleased]: https://github.com/FacileStudio/tronc/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/FacileStudio/tronc/releases/tag/v0.1.0
