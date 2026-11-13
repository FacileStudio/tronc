# Changelog

All notable changes to `tronc` are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow semver —
while on `v0`, a breaking change bumps the minor.

## [Unreleased]

## [0.23.0] - Unreleased

Rust: ported in tronc-rs v0.5.0; TS: ported in tronc-ts v0.3.0. Each entry names the port's
counterpart.

### Added

- **`mcp.AppSpec.Scopes`**, so a read-only server (sonar) goes through `NewApp` with
  `Scopes{Read: "<app>.read"}` instead of copying it. Rust: tronc-rs `App::new`. TS: has
  read-only servers already.
- **`mcp.StateKey(app, secret)`**: SHA-256 over `<app>-mcp-state:<secret>`, byte for byte the key
  the thirteen apps derived by hand, so signed questions survive the move. Vector
  `mcp_state_key.json`. Rust: `state_key`. TS: `stateKey`.
- **Bounded pending actions.** `Request` asks about the person's open action of the same kind,
  target and digest instead of inserting another, on the link and the MRTR paths.
  `PostgresPending.Open` reads at most `OpenLimit` + 1 rows; `Confirmations.List` keeps
  `OpenLimit` (100) and reports `truncated`, which `PendingList` carries as
  `"truncated": true`. `PostgresPending.Prune`, `PrunePending(ctx, exec, olderThan)` and
  `KeepPruned(ctx, store, every, olderThan, logger)` delete the actions resolved or expired
  more than 30 days ago, hourly. No migration or index. A Postgres-backed test pins that prune
  spares open, recently resolved and recently expired actions. The dedupe is best effort, and a
  reused action keeps its first client and expiry. Rust: dedupe, limit, prune. TS: same.
- **Confirmation text.** `Outcome.Message` carries the standard "Nothing happened yet…" text
  (`Confirmations.Waiting()`, naming the pending lifetime, or the time a reused action has left)
  on a link, and `MessageDeclined` on a declined `Resume`, the text eight apps each kept a copy
  of. Rust: ask/resume helper. TS: same.
- **`PendingPage.Mount(router, auth...)`** mounts the confirm page API at `/mcp/pending`
  (`PendingAPIPath`) behind the app's auth, nil-safe for docs tests. Rust: `PendingPage` router.
  TS: Go-only for now, tronc-ts has no confirm page.
- **Offset pager** `Page(items, cursor, size)`, `Offset(cursor)` and `Cursor(offset, total)`, the
  copies in Plume, Casier and Echo. Rust: pager. TS: same.
- **`mcp.PlanLimit`**: `Failure` maps any error with `PlanLimit() (feature, upgradeURL)`, such as
  porte's `forfait.LimitError` once it gains the method, to "The plan limit for <feature> is
  reached. Upgrade at <url>". Rust and TS: same text.
- **Typed Bulle AI calls.** `bulle.Client.Complete(ctx, CompleteRequest{System, Prompt, Input,
  Schema, MaxTokens, SpaceID})` for `POST /api/ai/complete` as the user, and
  `DecideAI(ctx, DecideRequest{ActingFor, State, Questions})` for `POST /api/ai/decide` as the
  app's service with `X-Bulle-Acting-For`. `SpaceID` is omitted when empty, so it is safe before
  Bulle accepts it. `Request.Header` adds headers to `Call`. `PlanRequired.Limit` carries a
  402's `limit`. Rust: `complete(space_id)`. TS: `complete`, `decideAI`.
- **Conformance vectors** for `mcp` and `bulle`: `mcp_http` (401 and 403 challenges, write gate,
  Origin refusal, 405, RFC 9728 metadata), `mcp_rate_limit` (default burst 120, burst equal to the
  per-minute limit, one bucket per user, none depending on replay speed), `mcp_digest`, `mcp_state_key`, `mcp_failure`, `bulle_errors` (402 bare and
  enveloped, upgrade URL filter, 4xx, 5xx, redirect) and `bulle_paths`. Their `source` is
  `tronc@v0.23.0`; the eight older files are unchanged. Rust: consume in CI. TS: same.

### Changed

- **`mcp.Failure`** keeps an `unavailable` error's message instead of the generic text, and
  re-checks a `payment_required` upgrade URL with `errors.SafeUpgradeURL`. Rust and TS: align.
- **`bulle.Client` never follows a redirect**: a 3xx fails as `ErrUnreachable`, so the token
  never leaves for another URL. It works on a copy of `Config.HTTPClient`. Rust: redirect-refusing
  client. TS: same.
- **Annotations rule** written into `mcp.Annotations` and docs/api.md: overwriting or removing
  the user's data, or silencing an alert, is `Destructive`; only a tool with no further effect
  on a repeat call is `Idempotent`. Docs only; Rust and TS: annotation presets.
- **build(deps)**: the root module requires `jackc/pgx/v5` v5.9.2 for the Postgres-backed prune
  test, which runs when `TEST_DATABASE_URL` is set (CI sets it), and moves `golang.org/x/text` to
  v0.41.0 and `golang.org/x/sync` to v0.22.0, the versions `testdb` already uses. No package
  outside the tests imports pgx.

### Fixed

- **`mcp.Failure` capitalised one byte**, so a message starting with an accented letter
  ("échec") came out garbled. It now uppercases the first rune. Rust and TS were already right.

## [0.22.2] - 2026-11-11

### Security

- build(deps): `github.com/go-chi/chi/v5` v5.2.3 to v5.5.0 in the root module (CVE-2026-72815,
  CVE-2026-72816, CVE-2026-72817, CVE-2025-69725, CVE-2026-114437). No API change; `migrate` and
  `testdb` are untouched and keep `migrate/v0.1.1` and `testdb/v0.1.2`.

## [0.22.1] - 2026-11-11

### Security

- build: Go 1.26.11 in the root, `migrate` and `testdb` modules (`go` directives, CI and mise
  pins). It fixes GO-2026-6848 to GO-2026-6859 in the standard library and toolchain, which
  douane reported as unscored gaps and which failed the gate.
- build(deps): `golang.org/x/sys` v0.41.0 to v0.44.0 (GO-2026-5024). In `testdb`:
  `jackc/pgx/v5` v5.6.0 to v5.9.2 (CVE-2026-33815, CVE-2026-33816, both critical) and
  `golang.org/x/text` v0.21.0 to v0.41.0 (GO-2026-5970, GO-2026-6629); `golang.org/x/crypto`
  leaves `testdb` because pgx v5.9.2 no longer needs it.
- Tags `migrate/v0.1.1` and `testdb/v0.1.2` carry the same change for the nested modules.

## [0.22.0] - 2026-11-11

### Added

- **`mcp.PendingPage[T]`, the confirm page API.** `Routes` serves `GET /`, `POST /{id}/confirm`
  and `POST /{id}/dismiss`, the handlers Courrier, Agenda, Nuage, Opus and Sablier each wrote in
  `modules/mcp/pending_routes.go`. The app passes how to read the signed-in person (`User`, which
  refuses a delegated token with the new `ErrDelegated`), the detail of each target
  (`Describe`, over the shared `PendingItem` its item type embeds) and the confirm result
  (`Done`). `PendingList[T]`, `PendingResult` and `EmptyRequest` replace the five copies of
  `types_pending.go`; `PendingRoutes[T](PendingDoc)` replaces the five `documentation.go`
  route lists.
- **`mcp.NewApp(AppConfig, AppSpec)`**, the wiring every app's `NewService` repeated: resource
  check, confirmations with the confirm page at `<origin>/pending`, and the server at
  `PerMinute` calls a minute (default 120). `AppConfig` is the five apps' identical `Config`.
- **`mcp.Digest` and `mcp.TargetIDs`.** The fingerprint all five apps computed (hex of the first
  16 bytes of SHA-256 over NUL-joined fields), byte for byte, so actions opened before an app
  adopts it still confirm; and the user-plus-numeric-target parse Agenda, Opus and Sablier
  shared.
- **Erasing pending actions.** `Pending.Erase(ctx, userIDs...)`, implemented by
  `PostgresPending` and `mcptest.Pending`, and `ErasePending(ctx, exec, userIDs...)` to run the
  same delete inside an account erasure's own transaction. Courrier, Nuage, Opus and Sablier
  each kept their own `DELETE FROM mcp_actions` for this.
- **apiref reflects embedded structs and generic types.** The fields of a struct embedded by
  value are promoted into the outer schema, as encoding/json promotes them, instead of being
  dropped (unexported type) or nested under the type name. An instance of a generic type gets a
  valid component name, `PendingList_Item`, instead of one holding brackets and package paths.
- **Conformance vectors.** `conformance/vectors/*.json` pins what `errors`, `httpjson`, `health`,
  the request log, the security headers, the client-IP rule, `spa` and `spa.InlineScriptHashes`
  answer today, in a JSON envelope (`source`, `subject`, `cases`) a port reads as its
  specification. `go test ./conformance` builds every case from the real packages and fails on
  any drift; `go test ./conformance -update` rewrites the files after a deliberate change.
  tronc-rs copies them at a tag.

### Changed

- **Breaking for custom stores:** `mcp.Pending` gains `Erase`. Every suite app uses
  `PostgresPending` or `mcptest.Pending`, which both implement it.
- Licence: tronc is now licensed under `MIT` (see `LICENSE`); before this, the repository carried no licence. The nested `migrate` and `testdb` modules carry the same file.

## [0.21.0] - 2026-11-10

### Added

- **`middleware.Limiter`, the rate limit bucket without HTTP.** `NewLimiter(RatelimitConfig)`
  builds the same token bucket `Ratelimit` uses. `Allow(key)` spends a token now; `AllowAt(key,
  now)` takes the clock and also returns the wait a refusal would send as `Retry-After`. It is
  for limits that run inside a handler or outside any request, such as Mycelium's login and
  Bulle's per-guest message budget.
- **`RatelimitConfig.MaxKeys`** bounds the keys a limiter remembers. Past the bound, the keys
  idle longest are forgotten first. Zero keeps every key, as before.
- **`RatelimitConfig.OnRefuse`** writes the refusal, for apps whose clients parse the JSON error
  envelope on 429. `Retry-After` is already set when it runs. Nil keeps the plain-text body and
  `StatusCode`.
- **`middleware.RemoteAddrKey64`**, a `KeyFunc` that buckets IPv6 callers by their /64, as
  httprate's `KeyByIP` did for Journal and Nuage.
- `docs/ratelimit.md`: what replaces each of the nine limiters written in the apps, and which
  stay app-side.

The zero value of every new field keeps the existing middleware's decisions, `Retry-After`
values, headers and body unchanged. `TestLimiterMatchesTheOriginalBucketDecisionForDecision`
replays 20 000 requests against a copy of the previous bucket to prove it.

## [0.20.0] - 2026-11-10

### Added

- **Read-only MCP servers.** `mcp.Options.Scopes` may set `Read` alone when every tool is
  read-only: the metadata then lists only that scope and the write gate is not mounted. A tool
  that is not read-only on such a server is refused at boot. `mcptest.Run` accepts metadata with
  one scope and fails when a server listing no write scope serves a write tool. sonar's search
  server is the first user, with `sonar.read` as its only scope.

### Fixed

- **`mcp.Failure` keeps plan refusals actionable.** A `payment_required` error, such as an
  exhausted daily quota, kept only the generic "could not finish" text. It now keeps its message
  and appends the upgrade page from `WithUpgradeURL`, so the model can send the user there.

## [0.19.0] - 2026-11-10

### Added

- **`mcp`, the remote MCP server kit, extracted from Courrier.** `mcp.Mount(router, Options)`
  serves the go-sdk v1.8.0 streamable transport in stateless mode at the resource's path, with
  the RFC 9728 metadata at its well-known path. In front of it: the Origin check (403), the
  bearer check with `error="invalid_token"` and `resource_metadata` in the 401 challenge, a 403
  `insufficient_scope` step-up challenge for a write tool called without the write scope, and a
  per-person rate limit. GET answers 405. List results are cached privately.
- **Typed tools with required annotations.** `mcp.NewTool(ToolSpec, handler)` builds a tool from a
  typed go-sdk handler. `Mount` refuses a tool without the `<app>_` prefix, a title, a
  description or all four hints, and any tool that is not read-only needs the write scope, at
  the HTTP gate and again inside the handler. Write calls are audited, without arguments.
- **`mcp.Confirmations`** asks a person before a sensitive action runs: MRTR `input_required`
  when the client declares form elicitation on 2026-07-28, otherwise a pending action and a
  confirm link. The question state is HMAC-signed and bound to the person, each action runs at
  most once (atomic claim), and a target that changed since the question closes the action.
  `mcp.PostgresPending` stores actions in `mcp_actions` (`mcp.PendingSchema`, additive).
- **Helpers:** `Failure` for actionable tool errors, `Detailed`, `EncodeCursor`/`DecodeCursor`,
  `FitCount`, `EncodedSize` and `Clip` for a 25k-token result budget.
- **`mcp/mcptest`, the conformance kit.** `mcptest.Run` checks the 401 challenge, the metadata,
  405 on GET, the Origin check, annotations, schemas, a stable sorted tool list, the write-scope
  challenge and each sensitive tool's confirmation path. `NewIssuer` is a fake authorization
  server and `NewPending` an in-memory store.

### Changed

- **The root module now requires `github.com/modelcontextprotocol/go-sdk` v1.8.0** beside chi
  (owner decision; see `docs/development.md`).

## [0.18.0] - 2026-11-10

### Added

- **`bulle`: a client for Bulle's Ideas API, and `MountIdeas`.** Every suite app is getting an
  Ideas page whose ideas Bulle generates and runs. `bulle.Client` covers the list, generate, run,
  run status, decide and dismiss routes, plus `Call` for later AI routes, with paths built from
  escaped segments. A 402 is `*PlanRequired` (bare body or envelope), other 4xx keep their status
  in `*StatusError`, and 5xx, timeouts and network errors wrap `ErrUnreachable`. The token comes
  from a `func(ctx) (string, error)`, so porte's exchange plugs in without tronc importing porte.
  `MountIdeas` serves `/ideas*` to the app's own frontend in a few lines, with the plan pre-check
  (`AIAccess`) and the active space (`Space`) supplied by the app. No new dependency.

## [0.17.1] - 2026-11-06

### Changed

- **`/docs` allows the Scalar bundle's exact URL, not all of jsDelivr.** `script-src` now names
  `https://cdn.jsdelivr.net/npm/@scalar/api-reference@1.64.0` (or the scheme, host and path of
  `Config.ScriptURL`). Allowing the host allowed every package anyone publishes there. Checked
  in headless Chromium under an enforced CSP: the reference renders, and only Scalar's
  `api.scalar.com` registry calls stay blocked. No worker or proxy source is needed; see
  `docs/api.md`.
- **An explicit `frame-ancestors 'none'` keeps `X-Frame-Options: DENY`.** Both `SecurityConfig`
  and `SecurityOverride` dropped it for any `frame-ancestors` source, including `'none'`. It is
  now dropped only when an origin is allowed to frame the page.

### Added

- **`middleware.MaxCSPHeaderBytes` (4096), and a startup warning past it.** `SecurityHeaders`
  logs through `slog.Default` when a CSP header outgrows what proxies reliably pass; nginx
  answers 502 when the headers overflow `proxy_buffer_size`. Each inline script hash costs 54
  bytes, so the default policy has room for about 70.

### Upgrade note for apps

- **tronc requires Go 1.26 since 0.17.0**, and 0.17.0's changelog did not say so. `go.mod` in
  the root, `migrate` and `testdb` moved to `go 1.26` (from `1.24.0`, `1.25.7` and `1.24.0` in
  0.16.0). An app still building on a `golang:1.25` image fails with `go.mod requires go >= 1.26`
  (or downloads a toolchain, where `GOTOOLCHAIN` allows it). Move the Dockerfile `FROM` and the
  CI `go-version` to 1.26 in the same change as the bump.

## [0.17.0] - 2026-11-04

### Added

- **Security headers on every response, on by default.** `httpx.NewRouter` and `httpx.Chain`
  now mount `middleware.SecurityHeaders`, between `Recoverer` and `RealIP`. With no
  configuration every response carries:

  - `Content-Security-Policy-Report-Only: default-src 'self'; base-uri 'self'; object-src 'none';
    frame-ancestors 'none'; form-action 'self'; img-src 'self' data: blob: https:;
    connect-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'`
  - `Content-Security-Policy: frame-ancestors 'none'; object-src 'none'; base-uri 'self'`,
    **enforced** while the full policy only reports, so framing is blocked from the first deploy
  - `X-Frame-Options: DENY`, unless the app opens `frame-ancestors`
  - `X-Content-Type-Options: nosniff`
  - `Referrer-Policy: strict-origin-when-cross-origin`
  - `Permissions-Policy: camera=(), microphone=(), geolocation=()`
  - `Cross-Origin-Opener-Policy: same-origin`
  - `Strict-Transport-Security: max-age=31536000; includeSubDomains`, only when the connection
    is TLS or a trusted proxy sends `X-Forwarded-Proto: https`. The trust set is the router's
    `TrustedProxies`, so a client cannot claim HTTPS by setting the header itself.

  `httpx.Config.Security` (`middleware.SecurityConfig`) extends it: `CSPEnforce`, `CSPSources`
  per directive, `CSPReportURI`, `Allow` for Permissions-Policy features,
  `CrossOriginOpenerPolicy` and `HSTSExcludeSubdomains`. Each header is set before the handler
  runs and only if nothing upstream set it, so a handler's `Header().Set` wins. A source that
  would inject a directive or split the header panics at startup, the same stance `CORS` takes.

- **`middleware.SecurityOverride(SecurityOverrideConfig)`** extends the chassis headers on one
  route, for an embed that must be framed or a page that needs the camera. It reads the outer
  CSP back and writes into the same headers, so the route keeps the app's mode, script hashes
  and `report-uri`. Only the first value of each CSP header is extended; a value another
  middleware added is kept as it is. Mode changes only through `CSPEnforce *bool`, and only upward: a route can
  promote report-only to enforced, never downgrade.

- **`spa.InlineScriptHashes`** returns a `'sha256-...'` source for every inline script in the
  built client. adapter-static writes two (the bootstrap, which changes every build, and
  `app.html`'s own), and `script-src 'self'` blocks both. Attributes are read the way the HTML
  tokenizer reads them, so a quoted `>` or `src=` cannot mis-hash, and folds case for ASCII
  only, so byte offsets stay exact whatever runes the page holds. Checked against Nuage's build
  in headless Chromium: with the hashes the client boots under an enforced CSP, without them
  Chromium blocks both scripts and names the same two hashes.

- **`errors.PaymentRequired`**, code `payment_required`, HTTP 402, for a feature or quota the
  caller's plan does not include. `(*Error).WithUpgradeURL` attaches where to upgrade, and
  `httpjson.WriteError` adds it to the envelope as `upgrade_url`. `errors.SafeUpgradeURL` keeps
  only a same-origin path or an `https` URL with a host and no credentials, and `WriteError`
  checks it again, so an `UpgradeURL` assigned directly cannot bypass it.

### Changed

- **`/docs` extends the app's CSP** through `SecurityOverride`: the Scalar bundle's origin in
  `script-src` and `https://fonts.scalar.com` in `font-src`, so the reference keeps rendering
  once an app enforces. Scalar's calls to `api.scalar.com` stay blocked.

### Upgrade note for apps

The full CSP starts in report-only. Three things are enforced from the first deploy and break
apps that are not ready, so ship the fix in the same upgrade:

- **Camera and microphone.** `Permissions-Policy` denies them. **Echo and Prisme** (both on
  livekit-client) need `Security: middleware.SecurityConfig{Allow: []string{"camera",
  "microphone"}}`, or calls lose their media. Field sets its own header after the chassis and
  keeps working.
- **Framing.** `frame-ancestors 'none'` and `X-Frame-Options: DENY` are enforced. **Echo's
  `/embed/`** keeps its own CSP but now also gets `X-Frame-Options: DENY`, which blocks the
  embed: mount `SecurityOverride` with `frame-ancestors` set to the embed origins on `/embed/`,
  and delete `FrameAncestors`. Any other page another origin frames (check the Plume and
  Ardoise embeds) needs the same override.
- **COOP.** `same-origin` cuts `window.opener` to a cross-origin popup. An app whose OAuth or
  payment popup reports back through `window.opener` needs `same-origin-allow-popups`. None was
  found; redirect flows are unaffected.

Then:

1. Delete the local `SecurityHeaders` middleware (Agenda, Courrier, Journal, Nuage, Plume),
   `X-Frame-Options` included: the chassis now sends everything they sent. Agenda's copy
   believed `X-Forwarded-Proto` from any peer; the chassis does not. Field: move its grants to
   `Allow: camera, microphone, display-capture, speaker-selection` before deleting `headers.go`.
2. Pass `spa.InlineScriptHashes(clientDir)` as `CSPSources["script-src"]` in every app that
   serves its SvelteKit client from the binary.
3. Add what the app loads from elsewhere. Echo and Prisme: the LiveKit origin (`wss://` and
   `https://`) in `connect-src`. Casier (`iconify-icon` from jsDelivr in `app.html`), and the
   Nuage and Capsule `/docs` client routes (Scalar from jsDelivr): `https://cdn.jsdelivr.net` in
   `script-src`, or move to tronc's `apiref`. Opus's YouTube embeds: `frame-src`.
4. Deploy, use every screen, and read the console for `[Report Only]` violations. Without
   `CSPReportURI` they appear in the console and nowhere else. When a release shows none, set
   `CSPEnforce: true`.

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
  putting goose in the root would hand it to all ten consumers — including Mycelium, which keeps
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

Both gaps surfaced while migrating Mycelium, the last Go app to adopt the chassis.

### Added

- `errors.NotAllowed` (`method_not_allowed` → 405) and `errors.Unavailable` (`unavailable` → 503).
  Without them `WriteError` collapsed both onto 500, so an app converting its handlers would have
  turned "this identity provider is down, retry" into "something broke, give up" — a real
  regression on the SSO-unavailable path.
- `env.LoadCoreWithout` — `LoadCore` for a service with no database of its own. `DATABASE_URL`
  is read if present and not required.

  `LoadCore` requiring it excluded two apps from the shared configuration: Mycelium, which keeps
  its state as files, and Agenda, which builds its DSN from `DB_USER`/`DB_PASSWORD`. Both had to
  hand-populate `Core` field by field, which is exactly the duplication this package exists to
  remove.

## [0.7.0] — 2026-08-05

### Added

- `httpx.Chain` — the standard middleware stack applied to any `http.Handler`, in the same order
  `NewRouter` uses.

  `NewRouter` returns a `*chi.Mux`, which shut out the one Go app in the suite that does not use
  chi: Mycelium routes with Go 1.22's `http.ServeMux` and pattern matching. Rewriting it onto chi
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

- Requires Go 1.24. Mycelium runs 1.26.1 and Plume and Casier run 1.25.0, but 1.24.0 is the
  floor across the suite.
- The only dependency is `github.com/go-chi/chi/v5`.

[Unreleased]: https://github.com/FacileStudio/tronc/compare/v0.22.0...HEAD
[0.22.0]: https://github.com/FacileStudio/tronc/compare/v0.21.0...v0.22.0
[0.21.0]: https://github.com/FacileStudio/tronc/compare/v0.20.0...v0.21.0
[0.20.0]: https://github.com/FacileStudio/tronc/compare/v0.19.0...v0.20.0
[0.19.0]: https://github.com/FacileStudio/tronc/compare/v0.18.0...v0.19.0
[0.18.0]: https://github.com/FacileStudio/tronc/compare/v0.17.1...v0.18.0
[0.17.1]: https://github.com/FacileStudio/tronc/compare/v0.17.0...v0.17.1
[0.17.0]: https://github.com/FacileStudio/tronc/compare/v0.16.0...v0.17.0
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

## [0.15.0] — 2026-08-23

### Added

- `middleware.Ratelimit` — the suite's canonical per-key token-bucket rate
  limiter. Eight apps hand-wrote one; this is the ninth and last. Continuous
  refill, no background goroutine, `Retry-After` on refusal, nil `KeyFunc`
  falls back to the address `RealIP` resolved. Entries are never evicted:
  bounded user bases behind Traefik make eviction policy someone else's bug.
