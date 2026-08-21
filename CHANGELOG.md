# Changelog

All notable changes to `tronc` are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow semver —
while on `v0`, a breaking change bumps the minor.

## [Unreleased]

Nothing yet.

## [0.14.1] - 2026-08-22

### Changed

- **`docs/api.md` warns that replacing the CORS header lists silently drops `X-Request-Id`.**
  Documentation only: no behaviour changed, no symbol moved.

  0.14.0 put `X-Request-Id` in `DefaultAllowedHeaders` and in the default `ExposedHeaders`, so an
  app that *extends* the defaults inherited the echo for free — Capsule writes
  `append(middleware.DefaultAllowedHeaders, "X-Delete-Token")`, which is the only reason it did.
  An app that assigns its own literal list instead loses the header with no error anywhere: reads
  keep working, the browser simply never gets to send or read the id, so a client-side error can
  no longer name the request the server logged. The same trap applies to `ExposedHeaders` — a
  response header a script cannot read is the same as one never sent.

  The warning already existed, in Journal's `CLAUDE.md`, where no other app author would ever read
  it. It belongs beside the `CORS` contract it qualifies.

## [0.14.0] — 2026-08-17

### Added

- **`middleware.RequestID`, replacing chi's, so a request id survives the round trip.** It
  accepts the caller's `X-Request-Id`, **echoes it on the response**, and stores it under chi's
  context key — `GetReqID`, `RequestLogger` and `Recoverer` keep working untouched, and
  `middleware.RequestIDFrom` reads it without importing chi.

  The echo is the feature. Journal's browser SDK mints an id before a request leaves the page so
  a failed fetch can name the id the server logged it under; without an echo the page can never
  learn the server's own id, and the correlation is one-way guesswork. `CORS` therefore also
  gained `ExposedHeaders` (default `X-Request-Id`) and lists `X-Request-Id` in
  `DefaultAllowedHeaders` — a response header is invisible to a script unless it is exposed, so
  the echo would otherwise arrive and be unreadable.

  Two defects in chi's version came out while wiring it, both of which shipped in every app on
  the chassis:

  - **The header was taken verbatim** — any bytes, any length. That value is written to every
    log line the request produces, stored by Journal as `meta.request_id`, and offered in the
    dashboard as a clickable filter. It is now bounded at `MaxRequestIDLength` (128) and limited
    to alphanumerics and `-_.:/ `; anything else is replaced with a freshly minted id rather than
    escaped. Same shape of bug as the `RealIP` one in 0.10.0: a header believed without being
    checked.
  - **chi's minted id embeds `os.Hostname()`** and a per-process counter. Harmless while nothing
    echoed it; a hostname disclosure the moment something did. Ours is `crypto/rand.Text()` and
    says nothing about the machine.

  **Behaviour change**, not an API break: ids look different, and a caller sending a malformed
  one now gets a minted id instead of seeing its own reflected in the logs. `ExposedHeaders`
  follows the `TrustedProxies` convention — `nil` is unset, a non-nil empty slice means none.

## [0.13.0] — 2026-08-10

### Fixed

- **An app whose API is served from the root logged nothing.** `RequestLoggerConfig.APIPrefix`
  rewrote `""` to `/api`, so "the API is at the root" was unsayable — and `Classify` then put
  every request in `KindStatic`, which is logged at the quiet level, which is
  indistinguishable from an app that logs no requests at all. Found on Vision, where the client
  container strips `/api` before proxying, so the API sees `/auth/me` and had been silent since
  it adopted the chassis.

  `APIPrefix` is a `*string` on both `RequestLoggerConfig` and the new `httpx.Config.APIPrefix`:
  nil means `/api`, and `middleware.RootAPI` (also re-exported as `httpx.RootAPI`) means the
  root. **Breaking** for anyone setting `APIPrefix` directly, which inside this repo was nobody.

  It is the same collapse as the `TRUSTED_PROXIES` and delivery-target cases: a zero value
  standing in for both "unset" and a legitimate answer. Health probes are matched before the
  prefix, so they stay quiet under either setting.

## [0.12.0] — 2026-08-10

### Added

- `middleware.RealIPWith(RealIPConfig{Trusted, CDN, Header})`, and `Core.CDNProxies` /
  `Core.CDNHeader` populated when `TRUSTED_PROXIES` names `cloudflare`. Naming the CDN is what
  opts in: "this app is served through Cloudflare" is both why the edge is trusted and why
  `Cf-Connecting-Ip` means anything.

  v0.11.0 was not enough on its own, and production said so. **Traefik does not extend an
  incoming `X-Forwarded-For` — it replaces it** with the address it received from. So behind
  Cloudflare the app sees a one-entry chain holding the edge, the walk consumes it, nothing is
  left, and `RemoteAddr` stayed the Docker-internal peer: measured on Journal after the v0.11.0
  deploy, `remote_addr` `10.0.1.30:43516` with the visitor present only in `Cf-Connecting-Ip`.

  So the visitor is now recovered from the CDN header — **on one condition**: the rightmost
  forwarded hop must itself be a CDN address. That is the proof the request entered through the
  CDN. Believing the header unconditionally would undo the entire package, because the origin is
  reachable directly (verified: `curl --resolve journal.facile.studio:443:65.108.2.107` answers
  200) and anything reaching it could set the header. A request that skipped the CDN carries its
  own sender as the rightmost hop, fails the check, and is treated as the client it is. There is a
  test for exactly that.

  A forwarded chain that still holds a real visitor wins over the header — the header is the
  fallback for a chain that lost it, not a preference.

## [0.11.0] — 2026-08-10

### Added

- `TRUSTED_PROXIES` accepts **named sets** beside literal CIDRs: `private` (the default) and
  `cloudflare`, so a deployment says what fronts it instead of pasting two dozen ranges into its
  environment. `TRUSTED_PROXIES=private,cloudflare`. `middleware.CloudflareProxies` holds the
  published edge ranges (15 IPv4 + 7 IPv6, fetched 2026-08-10).

  It exists because v0.10.0 exposed a second-order problem. Behind Cloudflare the forwarded chain
  is `[visitor, cf-edge]`, and with only the private ranges trusted the walk stops at the edge — so
  `RemoteAddr` became the *edge* address. Nothing is forgeable, but every visitor behind one edge
  shares a rate-limit bucket and one visitor moving between edges looks like several. The limits
  hold; they stop describing people. Measured on Journal: `remote_addr` `172.70.108.91`, a
  Cloudflare edge, against a `client_ip` of the real visitor.

  **Opt-in, not default.** An app that is not behind Cloudflare gains nothing, and a name in a
  config file is a statement about the deployment that the next person can read.

  Trusting a CDN normally invites the attack where someone points their own Cloudflare zone at your
  origin, so their traffic arrives from a trusted edge carrying a header they wrote. It does not
  work here, and the reason is `RealIP`'s right-to-left walk: Cloudflare *appends* the visitor's
  address rather than replacing the header, so the entries an attacker seeds sit to the left of the
  one Cloudflare wrote and are never reached. A scheme taking the first hop, or trusting
  `Cf-Connecting-Ip` outright, would hand them the forged value.

  The list is a snapshot, not a subscription: fetching it at boot would make startup depend on the
  network and turn a hijacked response into a trust bypass. A stale range degrades attribution for
  one edge; it opens nothing.

## [0.10.1] — 2026-08-10

### Changed

- Comments moved out of function bodies and into the doc blocks above them, across `errors`,
  `middleware`, `migrate`, `spa` and `testdb`. No behaviour change and no API change: the diff
  is comments and gofmt alignment only.

  It is the house rule applied to the chassis — an explanation that only exists three levels
  inside a function body is invisible to anyone reading the package through `go doc` or an
  editor's hover, which is where a shared library is actually read.

- `filet.yml` checks in the quality-gate configuration, with `gen.line.long` disabled because
  gofmt already owns line width.

## [0.10.0] — 2026-08-10

### Security

- **`httpx` no longer installs chi's `RealIP`, which made every per-IP rate limit in every app
  on this chassis bypassable.** chi's rewrites `RemoteAddr` from `X-Forwarded-For` on every
  request with no check on the peer, so any caller able to set a header handed itself a fresh
  identity. Measured on Journal before the fix: 70 requests carrying a rotating
  `X-Forwarded-For` were **all** accepted against a 60/min bucket that should have refused ten;
  70 from a fixed value refused ten as designed. Login, register and session limiters were
  affected in all ten consumers.

  `middleware.RealIP(trusted []netip.Prefix)` replaces it. It believes the header only from a
  trusted peer, and walks the chain right to left — skipping hops that are themselves trusted —
  so the entry a client pre-seeds sits to the left of the one the proxy appended and is never
  reached. An unparsable hop stops the walk rather than reaching past it.

  `httpx.Config.TrustedProxies` and `TRUSTED_PROXIES` configure it. **Unset keeps the current
  behaviour in every real deployment**: the default trusts loopback and the private ranges,
  which is what Traefik is. Adopting the bump therefore needs no configuration change — the
  only requests whose treatment changes are those arriving from a public address with a
  forwarded header, which is exactly the abuse case.

  Trusting nothing by default was considered and rejected: behind Traefik every request carries
  one private address, so per-IP limits would collapse into a single global bucket and the login
  limiter would lock out everyone at once. `TRUSTED_PROXIES=none` selects that behaviour
  explicitly for anyone who wants it, and narrowing the list to the proxy's own address closes
  the residual case where a neighbour on the private network speaks for a visitor.

  `middleware.ClientIP` is unchanged and still for logging only.

### Added

- `migrate/` — ordered database migrations, as a **separate module** with its own tags and its
  own history in [migrate/CHANGELOG.md](migrate/CHANGELOG.md). Nothing in the root module
  changes: importing `tronc` still pulls chi and nothing else.

  It is separate precisely so that stays true. Go modules are all-or-nothing per module path, so
  putting goose in the root would hand it to all ten consumers — including Jardin, which keeps
  its state in files and has no database at all — and would push the root's `go` directive from
  1.24 to goose's 1.25.7.

- `testdb/` — a real PostgreSQL for tests, one schema per test binary, as a second separate
  module. Lifted from Casier's `internal/testdb`, which was the only harness in the suite not
  testing against SQLite, and fixed in three places: `Truncate` no longer empties goose's ledger
  (it does not drop tables, so the next run replayed from version 0 and died on
  `relation already exists`), the DSN gains a `connect_timeout` (pgx has none, so a blackholed
  host blocks ~75s per attempt inside a pre-push hook), and the libpq key/value DSN form no
  longer gets a `?search_path=` appended to it.

  It needs gorm and the Postgres driver, which the root module must never have. Every Go repo in
  the suite already has both, so consumers gain nothing new.

- The quality gate now covers nested modules. `go build|vet|test ./...` and
  `golangci-lint run ./...` all stop at a directory carrying its own `go.mod`, silently and with
  a passing exit code, so `migrate` would have shipped with no gate whatsoever. `scripts/check.sh`
  loops over `$NESTED`, and CI runs a second job for it. Naming the module as an extra pattern
  (`golangci-lint run ./... ./migrate/...`) is not a fix — it logs
  `directory prefix migrate does not contain main module` and still exits 0.

  The root CI job stays pinned to Go 1.24 on purpose. Building the chassis only on a newer
  toolchain is how a 1.25-only construct would reach a consumer that is still on the floor.

### Fixed

- The `[Unreleased]` link still compared from `v0.8.0` and there was no `[0.9.0]` line at all.

## [0.9.0] — 2026-08-06

### Added

- `apiref` — the API reference every Go app was hand-rolling. It renders a route registry as
  OpenAPI 3.1 and serves it behind Scalar at `/docs`, with the spec at `/docs/openapi.json`.

  The registry types were **byte-identical across eight apps** (Agenda, Casier, Courrier,
  GoSvelteBoilerplate, Nuage, Plume, Sablier, Vision) and the converter differed only in its
  title string — which is how Agenda's spec came to be titled "Courrier API". Plume's was the
  most complete implementation and is the one extracted.

- `apiref.Undocumented` walks a live chi router and reports routes the registry does not
  describe, so a hand-written inventory cannot silently drift from the code it documents.

### Fixed

- The widely-copied converter emitted `security: [{bearerAuth: []}]` without ever declaring
  the scheme, and dropped path parameters and request bodies. `apiref` declares
  `components.securitySchemes` and emits both.

- The Scalar bundle is now pinned (`@scalar/api-reference@1.64.0`) rather than floating on
  `latest`, so a Scalar release cannot change every suite app's reference page without a
  commit. Override with `Config.ScriptURL` for air-gapped deployments.

## [0.8.0] — 2026-08-05

Both gaps surfaced while migrating Jardin, the last Go app to adopt the chassis.

### Added

- `errors.NotAllowed` (`method_not_allowed` → 405) and `errors.Unavailable` (`unavailable` → 503).
  Without them `WriteError` collapsed both onto 500, so an app converting its handlers would have
  turned "this identity provider is down, retry" into "something broke, give up" — a real
  regression on the SSO-unavailable path.
- `env.LoadCoreWithout` — `LoadCore` for a service with no database of its own. `DATABASE_URL`
  is read if present and not required.

  `LoadCore` requiring it excluded two apps from the shared configuration: Jardin, which keeps
  its state as files, and Agenda, which builds its DSN from `DB_USER`/`DB_PASSWORD`. Both had to
  hand-populate `Core` field by field, which is exactly the duplication this package exists to
  remove.

## [0.7.0] — 2026-08-05

### Added

- `httpx.Chain` — the standard middleware stack applied to any `http.Handler`, in the same order
  `NewRouter` uses.

  `NewRouter` returns a `*chi.Mux`, which shut out the one Go app in the suite that does not use
  chi: Jardin routes with Go 1.22's `http.ServeMux` and pattern matching. Rewriting it onto chi
  to gain request logging, panic recovery and CORS would have been a large change for no benefit
  of its own. `Chain` gives it the same behaviour and leaves its router alone.

## [0.6.0] — 2026-08-05

### Fixed

- `spa.Handler` answers **405** to anything that is not GET or HEAD, instead of falling back to
  the index document.

  Found while migrating Journal. Its collector posts to an internal URL; once the API moved
  behind `/api`, that POST fell through to the SPA catch-all and received **200 with an HTML
  body**. The shipper treats any 2xx as delivery, so it discarded every batch — the whole
  suite's log collection failing silently, with nothing in any log to show for it.

  A static build serves reads. Answering a write with the index document turns a routing
  mistake into silent data loss, and this is the shape of bug that survives a deploy check
  because every probe is a GET.

## [0.5.0] — 2026-08-05

Consequence of the mono-container shape: one binary now serves the API *and* the client's static
assets, so a single request logger sees both.

### Changed

- `middleware.RequestLogger` classifies every request and records a **`kind`** field —
  `api`, `health` or `static` — and logs each class at a level that matches its value:
  - **`api` at info**, as before.
  - **`static` and `health` at debug.** Bundle requests and a healthcheck firing every ten
    seconds are not signal. At info they would bury the API traffic in `docker logs` and, worse,
    ship thousands of asset lines a day into Journal — the shipping handler forwards whatever the
    underlying handler admits, and apps run at info, so debug records never leave the host.
  - **any 5xx at error**, whatever it was serving. A failing asset is still a failure.
- `middleware.RequestLoggerWith` takes the classification rules explicitly (`APIPrefix`,
  `QuietLevel`); `RequestLogger` is unchanged in signature and defaults to `/api` and debug.
- `middleware.Classify` is exported so an app can make the same distinction elsewhere.

Set `LOG_LEVEL=debug` on an app to see asset and probe traffic again.

## [0.4.0] — 2026-08-05

### Added

- `spa` — serves a built single-page application from a directory, so one Go binary can serve
  both the API and the client it belongs to. Extracted from Courrier's `internal/spa`, which was
  the only implementation in the suite, and hardened on the way:
  - the history fallback **does not** apply to paths carrying a file extension. A missing bundle
    now 404s instead of receiving `index.html` with a `text/html` content type, which surfaces
    in the browser as an unrelated MIME or syntax error and hides the real failure.
  - containment is delegated to `http.Dir`, so an encoded traversal cannot escape the directory.
  - dotfiles are refused outright.
  - hashed assets get `immutable` caching, the index document gets `no-cache`.

## [0.3.0] — 2026-08-05

### Added

- `httpjson.DecodeJSONLimit` and `httpjson.DecodeGzipJSONLimit` — the decoders with the byte
  caps given explicitly.

  Found by comparing the nine copies being replaced: eight cap request bodies at 1 MB, but
  **Journal caps at 8 MB with a 32 MB decompressed ceiling**, because its `/ingest` takes
  batches of up to 1000 log entries. Adopting a hardcoded 1 MB would have silently started
  rejecting real ingest traffic with a 413 — an outage in the service every other app ships
  its logs to, and one that only shows up under load.

  `DecodeJSON` and `DecodeGzipJSON` keep the 1 MB default, which is right for the other eight.

## [0.2.0] — 2026-08-05

Driven by the rollout survey across the remaining nine Go APIs: two gaps that would each have
broken a deploy.

### Added

- `httpjson.DecodeGzipJSON` — a gzip-compressed JSON body with two independent caps, the
  compressed body at `MaxBodyBytes` and the decompressed stream at the caller's limit. Bounding
  only the request is a decompression bomb. Journal's ingest needs this; it was the one function
  in the suite's nine `httpjson` copies that tronc did not cover.
- `errors.TooLarge` is now also returned when the decompressed cap is exceeded.

### Fixed

- `env.CORSOriginKeys` gained **`DOMAINS`** and **`DOMAIN`**. Five of the nine remaining apps
  read one of those two names — Agenda, Courrier and Sablier use `DOMAINS`, Plume and Vision use
  the singular `DOMAIN` — so without them adoption would have silently emptied the allowed-origin
  list and denied every cross-origin caller. Seven names are now read, in order.

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

- Requires Go 1.24. Jardin runs 1.26.1 and Plume and Casier run 1.25.0, but 1.24.0 is the
  floor across the suite.
- The only dependency is `github.com/go-chi/chi/v5`.

[Unreleased]: https://github.com/FacileStudio/tronc/compare/v0.14.1...HEAD
[0.14.1]: https://github.com/FacileStudio/tronc/releases/tag/v0.14.1
[0.14.0]: https://github.com/FacileStudio/tronc/releases/tag/v0.14.0
[0.13.0]: https://github.com/FacileStudio/tronc/releases/tag/v0.13.0
[0.12.0]: https://github.com/FacileStudio/tronc/releases/tag/v0.12.0
[0.11.0]: https://github.com/FacileStudio/tronc/releases/tag/v0.11.0
[0.10.1]: https://github.com/FacileStudio/tronc/releases/tag/v0.10.1
[0.10.0]: https://github.com/FacileStudio/tronc/releases/tag/v0.10.0
[0.9.0]: https://github.com/FacileStudio/tronc/releases/tag/v0.9.0
[0.8.0]: https://github.com/FacileStudio/tronc/releases/tag/v0.8.0
[0.7.0]: https://github.com/FacileStudio/tronc/releases/tag/v0.7.0
[0.6.0]: https://github.com/FacileStudio/tronc/releases/tag/v0.6.0
[0.5.0]: https://github.com/FacileStudio/tronc/releases/tag/v0.5.0
[0.4.0]: https://github.com/FacileStudio/tronc/releases/tag/v0.4.0
[0.3.0]: https://github.com/FacileStudio/tronc/releases/tag/v0.3.0
[0.2.0]: https://github.com/FacileStudio/tronc/releases/tag/v0.2.0
[0.1.0]: https://github.com/FacileStudio/tronc/releases/tag/v0.1.0
