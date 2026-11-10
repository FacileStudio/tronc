# tronc — API

The complete exported surface, package by package. Ten packages importable under
`github.com/FacileStudio/tronc/<package>`, plus `migrate`, which shares the path but is a
separate module with its own `go.mod` and tags. Behavior and rationale live in
[architecture.md](architecture.md); this page is the reference.

## `errors`

The suite error envelope: a stable machine-readable code, a human-readable message, and the
wrapped cause.

```go
type Error struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	UpgradeURL string `json:"upgrade_url,omitempty"`
	Cause      error  `json:"-"`
}

func (e *Error) Error() string                     // returns Message
func (e *Error) Unwrap() error                     // returns Cause, so errors.Is/As reach it
func (e *Error) WithUpgradeURL(rawURL string) *Error // sets UpgradeURL, returns e
```

`Cause` is `json:"-"`, so it never crosses the wire.

| Constructor | Code | Status |
|---|---|---|
| `Invalid(message string) *Error` | `invalid_argument` | 400 |
| `Unauthorized(message string) *Error` | `unauthenticated` | 401 |
| `PaymentRequired(message string) *Error` | `payment_required` | 402 |
| `Forbidden(message string) *Error` | `permission_denied` | 403 |
| `NotFound(message string) *Error` | `not_found` | 404 |
| `NotAllowed(message string) *Error` | `method_not_allowed` | 405 |
| `Conflict(message string) *Error` | `already_exists` | 409 |
| `Failed(message string) *Error` | `failed_precondition` | 412 |
| `TooLarge(message string) *Error` | `resource_exhausted` | 413 |
| `RateLimited(message string) *Error` | `rate_limited` | 429 |
| `Unavailable(message string) *Error` | `unavailable` | 503 |
| `Internal(message string, cause error) *Error` | `internal` | 500 |

`Internal` is the only constructor that takes a cause; the rest pass `nil`. Use
`New(code, message string, cause error) *Error` to attach a cause to any other code.

`PaymentRequired` is for a feature or quota the caller's plan does not include. Attach where to
upgrade with `WithUpgradeURL`, and `httpjson.WriteError` adds it to the envelope:

```json
{"error":{"code":"payment_required","message":"this plan has no seats left","upgrade_url":"https://ardoise.facile.studio/billing"}}
```

`WithUpgradeURL` keeps only a same-origin path (`/settings/plan`) or an absolute `https` URL with
a host and no credentials. Anything else (`javascript:`, `//evil.example`, `http:`, a control
character) leaves the field empty and the key absent, because a client renders it as a link.

`Status(err error) int` maps an error onto its HTTP status. Anything that is not an
`*errors.Error` — checked with `errors.As`, so a wrapped one still resolves — and any
unrecognized code both map to 500.

## `httpjson`

Bounded, strict request decoding and one way to write a response.

| Symbol | Signature |
|---|---|
| `MaxBodyBytes` | `const MaxBodyBytes int64 = 1 << 20` (1 MB) |
| `DecodeJSON` | `DecodeJSON(w http.ResponseWriter, request *http.Request, dst any) error` |
| `DecodeJSONLimit` | `DecodeJSONLimit(w http.ResponseWriter, request *http.Request, dst any, maxBytes int64) error` |
| `DecodeGzipJSON` | `DecodeGzipJSON(w http.ResponseWriter, request *http.Request, dst any, maxDecompressedBytes int64) error` |
| `DecodeGzipJSONLimit` | `DecodeGzipJSONLimit(w http.ResponseWriter, request *http.Request, dst any, maxCompressedBytes, maxDecompressedBytes int64) error` |
| `WriteJSON` | `WriteJSON(w http.ResponseWriter, status int, value any)` |
| `WriteError` | `WriteError(w http.ResponseWriter, err error)` |

`DecodeJSON` reads exactly one JSON object into `dst`: unknown fields are rejected, the body is
capped at `MaxBodyBytes`, and trailing data after the object is an error. It closes the body
and always returns an `*errors.Error` — `resource_exhausted` when a cap is hit,
`invalid_argument` otherwise.

`DecodeJSONLimit` is the same with an explicit cap, for endpoints whose payloads are
legitimately larger. Journal's `/ingest` is the case: 8 MB compressed, 32 MB decompressed,
because it takes batches of up to 1000 log entries. Prefer `DecodeJSON` everywhere else.

`DecodeGzipJSON` applies **two independent caps** — the compressed body at `MaxBodyBytes`, the
decompressed stream at `maxDecompressedBytes`. Both are needed: a small compressed body can
expand without limit, so bounding only the request is a decompression bomb.
`DecodeGzipJSONLimit` takes both explicitly. A body that is not valid gzip is `invalid_argument`.

`WriteJSON` sets `Content-Type: application/json` and the given status; a `nil` value writes
the status with no body. `WriteError` writes `{"error":{"code":...,"message":...}}` at the
status the code maps to, adds `upgrade_url` when the error carries one, and turns anything that is not an `*errors.Error` into a generic
`internal`, so a raw driver failure can never leak its text to a client.

## `logger`

```go
type Config struct {
	Level  string                          // debug | info | warn | error; anything else means info
	Output io.Writer                       // defaults to os.Stdout
	Wrap   func(slog.Handler) slog.Handler // called exactly once, at construction
}

func New(cfg Config) *slog.Logger
func ParseLevel(level string) slog.Level
```

The zero `Config` is valid and yields info-level JSON on stdout. `Wrap`, when set, replaces the
handler with whatever it returns — the seam that keeps log shipping out of `tronc`'s dependency
graph; see [architecture.md](architecture.md#why-journal-is-not-a-dependency). `ParseLevel` is
case- and whitespace-insensitive, accepts `warning` as well as `warn`, and returns
`slog.LevelInfo` for anything it does not recognize.

## `middleware`

### CORS

`CORS(cfg CORSConfig) func(http.Handler) http.Handler`

| `CORSConfig` field | Type | Default | Notes |
|---|---|---|---|
| `AllowedOrigins` | `[]string` | — | Matched exactly. Empty denies all cross-origin requests |
| `AllowCredentials` | `bool` | `false` | Sends `Access-Control-Allow-Credentials: true` |
| `AllowedHeaders` | `[]string` | `DefaultAllowedHeaders` | `Accept`, `Authorization`, `Content-Type`, `X-Request-Id` |
| `AllowedMethods` | `[]string` | `DefaultAllowedMethods` | `GET`, `POST`, `PUT`, `PATCH`, `DELETE`, `OPTIONS` |
| `ExposedHeaders` | `[]string` | `DefaultExposedHeaders` | `X-Request-Id`. `nil` is unset; a non-nil empty slice exposes none |
| `MaxAgeSeconds` | `int` | `600` | `Access-Control-Max-Age` |

`CORS` **panics at construction** when `AllowedOrigins` contains `*` and `AllowCredentials` is
set. Apps with a custom header — Capsule's `X-Delete-Token` — extend `AllowedHeaders`.

**Extend the defaults, do not replace them.** Capsule writes
`append(middleware.DefaultAllowedHeaders, "X-Delete-Token")`, which is why it inherited
`X-Request-Id` for free when 0.14.0 added it. An app that assigns its own literal list instead
silently drops `X-Request-Id`, and the failure is invisible: reads keep working, the browser
simply never gets to send or read the id, so a client-side error can no longer name the request
the server logged. The same applies to `ExposedHeaders` — a response header a script cannot read
is the same as one never sent.

### Security headers

```go
func SecurityHeaders(cfg SecurityConfig) func(http.Handler) http.Handler
func SecurityOverride(cfg SecurityOverrideConfig) func(http.Handler) http.Handler
func BuildCSP(extra map[string][]string, reportURI string) (string, error)
func IsHTTPS(request *http.Request, trusted []netip.Prefix) bool
```

`httpx` mounts `SecurityHeaders(Config.Security)` on every app. The zero `SecurityConfig` sends:

| Header | Value |
|---|---|
| `Content-Security-Policy-Report-Only` | `default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; img-src 'self' data: blob: https:; connect-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'` |
| `Content-Security-Policy` | `frame-ancestors 'none'; object-src 'none'; base-uri 'self'`, enforced while the full policy only reports |
| `X-Frame-Options` | `DENY`, unless `CSPSources` opens `frame-ancestors` to an origin. An explicit `'none'` keeps it |
| `X-Content-Type-Options` | `nosniff` |
| `Referrer-Policy` | `strict-origin-when-cross-origin` |
| `Permissions-Policy` | `camera=(), microphone=(), geolocation=()` |
| `Cross-Origin-Opener-Policy` | `same-origin` |
| `Strict-Transport-Security` | `max-age=31536000; includeSubDomains`, only when `IsHTTPS` |

| `SecurityConfig` field | Type | Default | Notes |
|---|---|---|---|
| `CSPEnforce` | `bool` | `false` | `true` sends the full policy as `Content-Security-Policy`. `false` reports it and enforces only its `frame-ancestors`, `object-src` and `base-uri` |
| `CSPSources` | `map[string][]string` | — | Extra sources per directive. Extends the default, replaces a `'none'` default, appends a directive the default lacks (`frame-src`, `media-src`, `font-src`, `worker-src`) in name order |
| `CSPReportURI` | `string` | — | Appends `report-uri`. Without it a report-only violation shows in the browser console only |
| `Allow` | `[]string` | — | Permissions-Policy features granted to `(self)` |
| `CrossOriginOpenerPolicy` | `string` | `same-origin` | Also `same-origin-allow-popups`, `noopener-allow-popups`, `unsafe-none` |
| `HSTSExcludeSubdomains` | `bool` | `false` | Drops `includeSubDomains`, for a host whose subdomains are not all on HTTPS |
| `Trusted` | `[]netip.Prefix` | the router's trusted proxies, filled by `httpx` | Peers whose `X-Forwarded-Proto` is believed; nil trusts none |

`IsHTTPS` is true when the connection is TLS, or when the peer is in `trusted` and the rightmost
`X-Forwarded-Proto` value, the one the nearest proxy wrote, is `https`. Plain HTTP in local
development gets no HSTS.

**The framing guard does not wait for the rollout.** A report-only CSP blocks nothing, so the
chassis also enforces the policy's `frame-ancestors`, `object-src` and `base-uri` in a
`Content-Security-Policy` of their own, plus `X-Frame-Options: DENY`. Both CSP headers apply;
the enforced one never gains a directive the report-only one lacks, so it cannot block what is
still being tuned. Apps can delete their local `X-Frame-Options` middleware.

**COOP and popups.** `same-origin` severs `window.opener` between the app and a cross-origin
popup. An OAuth or payment popup that reports back through `window.opener` needs
`same-origin-allow-popups`; a redirect flow needs nothing.

`style-src` carries `'unsafe-inline'` on purpose: Svelte transitions and SvelteKit's markup write
inline styles, and a style cannot run code. `script-src` does not.

`connect-src 'self'` covers same-origin `ws:` and `wss:` in current browsers, so a WebSocket or
SSE endpoint on the app's own host needs nothing. A different host (LiveKit) needs its origin.

**A handler wins.** Each header is set before the handler runs and only when nothing upstream
set it, so `w.Header().Set(...)` in a handler replaces it and `w.Header().Del(...)` removes it.
Use `Set`, not `Add`: two CSP headers are both enforced, so an added one narrows, never widens.

**Per route**, mount `SecurityOverride` on the route or group. It **extends** what the chassis
sent rather than rebuilding it: it reads the outer CSP headers back, adds its sources, and writes
into the same headers, so the route keeps the app's mode, inline script hashes and `report-uri`.

```go
router.With(middleware.SecurityOverride(middleware.SecurityOverrideConfig{
	CSPSources: map[string][]string{"frame-ancestors": {"https://ardoise.facile.studio"}},
})).Get("/embed/{id}", handler)
```

| `SecurityOverrideConfig` field | Effect |
|---|---|
| `CSPSources` | Extends the full policy. Extends the enforced framing policy only in the directives it already has. Opening `frame-ancestors` to an origin also drops `X-Frame-Options`; an explicit `'none'` keeps it |
| `CSPEnforce *bool` | `true` promotes a report-only route to enforced. `false` on an enforced app is ignored: a route never downgrades |
| `Allow` | Grants more Permissions-Policy features to `(self)` |
| `CrossOriginOpenerPolicy` | Replaces the outer value when set |

A route with no CSP from the chassis gets none: there is nothing to extend.

`SecurityHeaders` and `SecurityOverride` **panic at construction**, and `BuildCSP` returns an
error, on a source containing `;`, `,`, whitespace or a control character, a directive or
feature name outside `[a-z-]`, or an unknown COOP value. Each of those would inject a directive
or split the header.

**SvelteKit's inline scripts.** adapter-static writes two inline scripts into `index.html`: the
bootstrap that imports the entry chunks, whose content changes on every build, and anything
`app.html` carries, such as a theme script. `script-src 'self'` blocks both, and the page stays
blank. Pass their hashes:

```go
hashes, err := spa.InlineScriptHashes(clientDir)
cfg.Security.CSPSources = map[string][]string{"script-src": hashes}
```

Hashes, not nonces, and computed by the server, not by `kit.csp`:

- A nonce needs the HTML rewritten per request, and SvelteKit forbids nonces on prerendered
  pages. The built files are static; a hash fits them and keeps them cacheable.
- `kit.csp` hashes only the scripts SvelteKit generates, not the ones in `app.html`, and
  delivers the policy in a `<meta>` tag, where `frame-ancestors` and `report-uri` are ignored
  (https://svelte.dev/docs/kit/configuration#csp). A header CSP and a meta CSP are both
  enforced, so the header must allow the scripts anyway.

Checked against Nuage's real build in headless Chromium: with the hashes and `CSPEnforce: true`
the client boots with no violation; without them Chromium blocks both scripts and names the
same two hashes. An adapter-node client renders its HTML in Node, so the chassis headers never
reach those pages: configure `kit.csp` with `mode: 'auto'` there.

**Staged rollout.** Every app starts in report-only, which is the default:

1. Upgrade. Add `CSPSources` for what the app loads (inline script hashes, LiveKit, an embed
   parent). Optionally point `CSPReportURI` at a collector; tronc ships none, because an
   unauthenticated endpoint that writes logs needs a size cap and a rate limit of its own.
2. Deploy and use every screen. Read the browser console, or the reports, for `[Report Only]`
   violations. Fix each by adding the source or changing the code.
3. When a release shows no violation, set `CSPEnforce: true`.

The other headers, and the framing policy, are enforced from the first deploy.

### Request ID

| Symbol | Signature |
|---|---|
| `RequestID` | `RequestID(next http.Handler) http.Handler` |
| `RequestIDFrom` | `RequestIDFrom(ctx context.Context) string` |
| `RequestIDHeader` | `const = "X-Request-Id"` |
| `MaxRequestIDLength` | `const = 128` |

Accepts the caller's `X-Request-Id` when it is at most `MaxRequestIDLength` bytes of
alphanumerics and `-_.:/ `, mints an opaque id when it is not, echoes the result on the response,
and stores it under chi's context key so `GetReqID` keeps working.

Accepting a caller's id is what makes a browser error reachable from the server logs: the
`@facile/journal` SDK mints one before the request leaves the page, so a failed fetch can name
the id the server logged it under. That makes a request id **a hint, never a credential** — it is
caller-controlled by design, which is why it is bounded and charset-checked before it reaches a
log line, and why nothing may authorize on it.

### Request logging

| Symbol | Signature |
|---|---|
| `RequestLogger` | `RequestLogger(logger *slog.Logger) func(http.Handler) http.Handler` |
| `RequestLoggerWith` | `RequestLoggerWith(logger *slog.Logger, cfg RequestLoggerConfig) func(http.Handler) http.Handler` |
| `RequestLoggerConfig` | `{ APIPrefix *string; QuietLevel slog.Level }`, defaulting to `/api` and `slog.LevelDebug`. `middleware.RootAPI` is the value for an API served from the root — `""` and nil are different answers |
| `Kind` | `string` alias, with `KindAPI` `"api"`, `KindHealth` `"health"`, `KindStatic` `"static"` |
| `Classify` | `Classify(path, apiPrefix string) Kind` |
| `ClientIP` | `ClientIP(request *http.Request) string` |
| `RedactQuery` | `RedactQuery(rawQuery string) string` |
| `SensitiveQueryKeys` | `access_token`, `api_key`, `apikey`, `code`, `id_token`, `key`, `password`, `refresh_token`, `secret`, `signature`, `token` |

One record per request, message `http request`, with the fields every Facile app agreed on:
`kind`, `request_id`, `method`, `path`, `query`, `remote_addr`, `client_ip`, `user_agent`,
`status`, `bytes`, `duration`. `api` logs at info, `health` and `static` at `QuietLevel`, and
any 5xx at error.

`Classify` returns `KindHealth` for exactly `/health`, `/ready`, `<prefix>/health` and
`<prefix>/ready`; `KindAPI` for the prefix itself or anything under it; `KindStatic` otherwise.
It is exported so an app can make the same distinction elsewhere.

`RedactQuery` replaces the value of every `SensitiveQueryKeys` parameter, matched
case-insensitively, and returns the re-encoded query. A query it cannot parse comes back as
`[unparsable]` rather than raw; a query with nothing to redact is returned unchanged.

`ClientIP` reads `Cf-Connecting-Ip`, then the first `X-Forwarded-For` hop, then `RemoteAddr`.
Both headers are client-controlled unless a trusted proxy overwrites them, so **never** key a
rate limiter or an authorization decision on this value — use `RemoteAddr`, which `RealIP`
below resolves for exactly that purpose.

### Real IP

| Symbol | Signature |
|---|---|
| `RealIP` | `RealIP(trusted []netip.Prefix) func(http.Handler) http.Handler` |
| `DefaultTrustedProxies` | loopback, RFC1918, link-local, ULA — the `private` set |
| `CloudflareProxies` | Cloudflare's published edge ranges — the `cloudflare` set, opt-in |
| `ParseTrustedProxies` | `ParseTrustedProxies(values []string) ([]netip.Prefix, error)` |
| `TrustedBy` | `TrustedBy(addr netip.Addr, trusted []netip.Prefix) bool` |

Rewrites `RemoteAddr` from `X-Forwarded-For` **only when the peer is one of `trusted`**. That
is the whole difference from chi's middleware of the same name, and it is not cosmetic: chi's
rewrites on every request whatever the peer, so anything able to set a header hands itself a
fresh identity and every per-IP rate limit downstream becomes decorative. Measured on Journal
before this landed — 70 requests carrying a rotating `X-Forwarded-For` were all accepted
against a 60/min bucket that should have refused ten.

The header is walked **right to left**, skipping hops that are themselves trusted proxies, and
the first hop that is not becomes `RemoteAddr`. Each proxy appends the address it received
from, so the rightmost entry is the one written by the nearest hop — the only entry no client
could have forged — and walking left from there survives a chain without believing anything
the client wrote. An unparsable hop stops the walk: everything to its left was written by
whoever wrote it.

A nil or empty `trusted` ignores the header entirely. `httpx.Config.TrustedProxies` is where
apps set this; see [configuration.md](configuration.md).

### Recovery

```go
func Recoverer(logger *slog.Logger) func(http.Handler) http.Handler
```

Turns a panic below it into a logged 500 carrying the suite error envelope, with the request
ID, method, path, panic value and full stack. `http.ErrAbortHandler` is re-panicked, as
`net/http` expects.

## `httpx`

```go
type Config struct {
	Logger   *slog.Logger              // defaults to slog.Default()
	CORS     middleware.CORSConfig     // applied only when AllowedOrigins is non-empty
	Security middleware.SecurityConfig // always applied; the zero value is the default policy
}

func NewRouter(cfg Config) *chi.Mux
func Chain(cfg Config, next http.Handler) http.Handler
```

Both apply `RequestID → Recoverer → SecurityHeaders → RealIP → CORS → RequestLogger`,
outermost first. `Security.Trusted` defaults to the router's trusted proxies.
`NewRouter` returns a chi router to build on; `Chain` wraps any `http.Handler`, for an app that
routes with something other than chi.

## `health`

```go
const DefaultTimeout = 2 * time.Second

type Check func(context.Context) error

func DB(db *sql.DB) Check
func Live() http.HandlerFunc
func Ready(checks ...Check) http.HandlerFunc
func Mount(router chi.Router, checks ...Check)
```

| Route | Body | Status |
|---|---|---|
| `GET /health`, `GET /api/health` | `{"status":"ok"}` | 200 |
| `GET /ready`, `GET /api/ready` | `{"status":"ready"}` / `{"status":"not_ready"}` | 200 / 503 |

`Live` touches no dependency. `Ready` runs every check under `DefaultTimeout` and fails at the
first error. `DB` returns a `Check` that calls `db.PingContext`. `Mount` registers all four
routes — the `/api` copies exist because the public edge forwards only `/api/*` on some
deployments.

## `healthcheck`

```go
const Timeout = 3 * time.Second

func Handle(args []string) bool
func Probe(port string) error
```

`Handle` runs the probe and exits when `args[1] == "healthcheck"`, and returns `false` for a
normal start so `main` can continue. Call it as the first statement in `main`. It exits 0 on
success and 1 with a message on stderr on failure.

`Probe` requests `/health` on `127.0.0.1:<port>` and reports whether it answered 2xx. Not
`localhost`: in a distroless container `localhost` resolves to `::1` first while the server
binds `0.0.0.0`. `Handle` reads the port from `PORT`, defaulting to `8080`.

## `spa`

```go
const DefaultDir = "./client"

var ImmutablePrefixes = []string{"/_app/immutable/", "/assets/", "/_nuxt/"}

type Config struct {
	Dir   string // defaults to DefaultDir
	Index string // defaults to "index.html"
}

func Handler(cfg Config) http.Handler
func DirFromEnv() string
func Available(dir string) bool
func InlineScriptHashes(dir string) ([]string, error)
```

`InlineScriptHashes` walks `dir` for `.html` files and returns a sorted, deduplicated
`'sha256-...'` source for each inline `<script>` without a `src`. It reads attributes the way
the HTML tokenizer does, so a quoted value holding `>` or `src=` cannot end the tag early or pass
for a `src`. Hashes are taken after the CRLF normalisation the browser applies before hashing.

The hashes all land in one header, and proxies cap response headers (nginx holds all of them in
a 4 KB or 8 KB `proxy_buffer_size` by default, and answers 502 past it). Each hash costs 54
bytes, so the default policy has room for about 70 before `SecurityHeaders` warns at startup
that the CSP exceeds `MaxCSPHeaderBytes` (4096). An SPA build has two; a prerendered site with
one distinct inline script per page is the case that grows. Past the limit, move inline code
into files under `script-src 'self'` rather than raising the proxy buffer. Pass it as the `script-src` entry of
`SecurityConfig.CSPSources`; see [Security headers](#security-headers).

`Handler` serves static files from `cfg.Dir` and falls back to the index document for anything
that does not resolve. In order, per request:

1. Anything that is not GET or HEAD gets **405** with `Allow: GET, HEAD`. A static build
   answers reads and nothing else; answering a write with the index document turns a routing
   mistake into silent data loss.
2. A path whose base name starts with `.` gets 404.
3. An existing file is served with `Cache-Control: public, max-age=31536000, immutable` under
   an `ImmutablePrefixes` prefix, or `public, max-age=0, must-revalidate` otherwise.
4. A path with a file extension that did not resolve gets **404**, not the index — a missing
   bundle must not arrive as HTML.
5. Anything else gets the index document with `Cache-Control: no-cache` and
   `Content-Type: text/html; charset=utf-8`, served through `http.ServeContent`.

Containment is delegated to `http.Dir`, so a traversal attempt cannot escape the directory
however the path is encoded. `DirFromEnv` reads `CLIENT_DIR`, falling back to `DefaultDir`.
`Available` reports whether a directory holds an `index.html` file, so an app built without a
client can skip mounting the handler entirely.

## `env`

```go
type Core struct {
	AppEnv             Environment // Development | Staging | Production
	Port               int
	LogLevel           string
	DatabaseURL        string
	CORSAllowedOrigins []string
	JournalURL         string
	JournalToken       string
}

func (c Core) IsProduction() bool
```

| Symbol | Signature |
|---|---|
| `LoadCore` | `LoadCore() (Core, error)` |
| `LoadCoreWithout` | `LoadCoreWithout() (Core, error)` |
| `Environment` | `string` alias, with `Development`, `Staging`, `Production` |
| `ParseEnvironment` | `ParseEnvironment(value string) Environment` |
| `CORSOrigins` | `CORSOrigins() []string` |
| `CORSOriginKeys` | `CORS_ALLOWED_ORIGINS`, `ALLOWED_ORIGINS`, `DOMAINS`, `DOMAIN`, `CORS_ORIGINS`, `TRUSTED_ORIGINS`, `CLIENT_ORIGIN` |
| `String` | `String(key, fallback string) string` |
| `Required` | `Required(key string) (string, error)` |
| `Int` | `Int(key string, fallback int) (int, error)` |
| `Bool` | `Bool(key string, fallback bool) (bool, error)` |
| `Duration` | `Duration(key string, fallback time.Duration) (time.Duration, error)` |
| `List` | `List(key string) []string` |

`LoadCore` requires `DATABASE_URL` and errors when it is unset; `LoadCoreWithout` reads it if
present and does not require it. Both error on an unparsable `PORT`. Apps embed `Core` in their
own config struct, conventionally importing the package as `troncenv`.

`CORSOrigins` reads the first `CORSOriginKeys` entry that is set to a non-blank value and
splits it on commas. It does **no** scheme normalization, and `middleware.CORS` matches origins
exactly — a schemeless `DOMAIN` produces a list that can never match a browser `Origin`.

Every helper trims whitespace and treats a blank value as unset. `Int`, `Bool` and `Duration`
return an error on an unparsable value rather than falling back to the default — `Bool` accepts
only the `strconv.ParseBool` spellings, so `yes` and `on` are errors. Callers exit 1 on that
error, by convention across the suite.

Full variable reference and the adoption traps: [configuration.md](configuration.md).

## `apiref`

Serves a Facile API's reference documentation: an OpenAPI 3.1 document generated from a
hand-written route registry, behind a Scalar UI. Every suite backend answers on the same
path, so `/docs` means the same thing everywhere.

```go
apiref.Mount(router, apiref.Config{
	Title:       "Sablier API",
	Description: "Self-hosted time tracker for small teams.",
	Servers:     []string{"/api"},
	Registry:    docs.Registry,
})
```

That registers `GET /docs` and `GET /docs/openapi.json`. Mount it on the **root** router,
beside `/api` and before the SPA catch-all, so the reference sits at `/docs` rather than
behind the API prefix. Mounting it on a subrouter works and the page's `data-url` follows —
the spec URL is derived from the request path, not hardcoded.

| Symbol | What it is |
|---|---|
| `Config` | `Title`, `Version`, `Description`, `Servers`, `Registry`, `ScriptURL` |
| `Registry` / `Module` / `Route` / `Field` / `Error` | the route inventory types |
| `Mount(chi.Router, Config)` | registers the page and the spec |
| `OpenAPI(Config) map[string]any` | the document alone, for tests or static export |
| `Undocumented(chi.Routes, Config, ...string) []string` | routes the registry forgot |
| `ScalarScriptURL` | the pinned Scalar bundle |
| `BasePath`, `SpecPath`, `DefaultVersion` | `/docs`, `/docs/openapi.json`, `1.0.0` |

The generated document declares the `bearerAuth` security scheme it references, emits path
parameters from `Route.PathParams`, and attaches request and response bodies where the
registry names them. A `Route.Auth` of `""` documents a public route. A `ResponseBody` of
`[]Thing` becomes an array schema.

### Keeping the registry honest

A hand-written inventory drifts the moment someone adds an endpoint and forgets it — the
failure mode behind most stale documentation in this suite. `Undocumented` walks the live chi
router and reports registered routes the registry does not describe, so the drift becomes a
failing test rather than a lie:

```go
func TestEveryRouteIsDocumented(t *testing.T) {
	if missing := apiref.Undocumented(buildRouter(), refConfig()); len(missing) > 0 {
		t.Errorf("routes missing from the API registry: %v", missing)
	}
}
```

It ignores the reference page itself, `/health` and `/ready` at both mount points, and any
wildcard route; pass extra prefixes to ignore more. Registry paths are matched both as
written and joined onto each `Servers` entry, so an inventory of `/projects` matches a router
serving `/api/projects`. Regex-constrained chi parameters (`{id:[0-9]+}`) are normalized to
`{id}` before comparison.

### The Scalar bundle is a remote script

The reference page loads Scalar from a pinned jsDelivr build — pinned so a Scalar release
cannot change every suite app's page without a commit. That is one remote request on a
developer-facing page, but it does mean `/docs` renders blank on an air-gapped deployment.
Set `Config.ScriptURL` to a locally served copy where that matters. Nothing else in tronc
reaches the network.

The page mounts `SecurityOverride` adding the bundle's **exact URL** to `script-src` (scheme,
host and path; query and fragment dropped) and `ScalarFontOrigin` (`https://fonts.scalar.com`)
to `font-src`, so `/docs` keeps rendering once an app enforces, in the app's mode and with its
other sources. It names the file, not `https://cdn.jsdelivr.net`: a CDN serves anyone's
packages, so allowing the host would allow every script published there. A relative
`ScriptURL` needs nothing beyond `'self'`. It adds no CSP when the app sent none.

Checked in headless Chromium under an enforced CSP: the reference renders, and the only
violations are Scalar's calls to `api.scalar.com/vector/registry` (its public API registry),
which stay blocked and cost nothing. No `worker-src` is needed: the bundle starts no worker.
"Test request" sends the call straight to the documented server, so a same-origin API is
covered by `connect-src 'self'`; an API on another origin needs that origin in `connect-src`.
The bundle only defaults to `https://proxy.scalar.com` in Scalar's own web client layout, not
in the reference, so requests and their credentials do not pass through Scalar.

## `bulle`

A client for Bulle, called as the signed-in user, and the `/api/ideas` routes an app's own
frontend uses. It needs only `net/http` and chi, and it does not import porte: the token comes
from a `TokenSource func(ctx) (string, error)`, which is `porte/exchange`'s `Source.Token` in
practice (audience `bulle`, scopes `bulle.read` and `bulle.write`).

- `New(Config{URL, TokenSource, Forget, HTTPClient})`. `Forget` is optional and runs after a 401,
  before the one retry. The default HTTP client has a 20 s timeout.
- `Ideas(ctx, app, space)`, `Generate(ctx, app, space)`, `Run(ctx, ideaID, space)`,
  `RunStatus(ctx, runID)`, `Decide(ctx, runID, callID, approve)`, `Dismiss(ctx, ideaID)`.
  `space` is a Registre shared space id, or `""` for the personal space.
- `Call(ctx, Request{Method, Segments, Query, Body}, out)` for any other route. Each segment is
  escaped on its own; an empty, `.` or `..` segment is `ErrBadSegment` and sends nothing.
- Errors: a 402 is `*PlanRequired{Message, UpgradeURL, Key}`, read from a bare body or from the
  `{"error":{...}}` envelope (an unsafe `upgrade_url` is dropped). Any other 4xx is
  `*StatusError{Status, Code, Message}`. A 5xx, a timeout or a network error wraps
  `ErrUnreachable`.
- `RunStatus` adds `State` (`running`, `needs_approval`, `done`, `failed`) and `ContinueURL`, clears
  output URLs that are not http or https, and never carries Bulle's `decide_path`.

`MountIdeas(router, Options{App, URL, TokenSource, Forget, HTTPClient, Space, AIAccess, UpgradeURL})`
mounts, on the router the app's API lives on:

| Route | Does |
|---|---|
| `GET /ideas` | `{ai_access, upgrade_url, ideas, generating, last_batch_at}`; no Bulle call without AI access |
| `POST /ideas/generate` | starts a batch; 402 without AI access |
| `POST /ideas/{id}/run` | starts a background run in the active space; 402 without AI access |
| `POST /ideas/{id}/dismiss` | 204 |
| `GET /ideas/runs/{run}` | the run |
| `POST /ideas/runs/{run}/approvals/{call}` | `{"approve": bool}`, 204; approving needs AI access, declining does not |

Mount it behind the app's authentication middleware. A Bulle 401 is about the app's token, so the
frontend gets 503, never 401. A token source error that is a tronc `*errors.Error` (porte's
`ErrReconnect` is a 412) is passed through as it is.
## `mcp`

A remote MCP server for one app: go-sdk v1.8.0, streamable HTTP, stateless (spec 2026-07-28).

```go
func Mount(router chi.Router, opts Options) error
func NewTool[In, Out any](spec ToolSpec, handler sdk.ToolHandlerFor[In, Out]) Tool
func CallerOf(req *sdk.CallToolRequest) (Caller, error)
func Failure(err error, hint string) error
func Audit(ctx context.Context, logger *slog.Logger, e Event)

func NewConfirmations(cfg ConfirmConfig) (*Confirmations, error)
func (c *Confirmations) Request(ctx, req, kind, target, question string) (*sdk.CallToolResult, Outcome, error)
func (c *Confirmations) Resume(ctx, req, kind string) (Outcome, error)
func (c *Confirmations) Confirm(ctx, who Caller, id string) (Outcome, error)
func (c *Confirmations) Dismiss(ctx, who Caller, id string) error
func (c *Confirmations) Open(ctx, userID string) ([]Action, error)
func (c *Confirmations) State(ctx, a Action) string
func CanElicit(req *sdk.CallToolRequest) bool
func Retrying(req *sdk.CallToolRequest) bool

func Detailed(format string) (bool, error)
func EncodeCursor(position any) string
func DecodeCursor(cursor string, position any) error
func FitCount(total int, size func(n int) int) int
func EncodedSize(v any) int
func Clip(text string, limit int) (string, bool)
```

`Mount` serves the endpoint at the path of `Options.Resource` and the RFC 9728 document at
`/.well-known/oauth-protected-resource<path>` and the bare well-known path. Mount it on the root
router, outside `/api` and outside any CSRF middleware: MCP clients send a bearer token, never a
cookie. The order in front of the transport is Origin check, bearer check, write-scope gate,
rate limit.

- **Tokens.** `Options.Verifier` must refuse a token whose audience is not the resource. In the
  suite it is `porte/mcpauth.Verifier`; tronc never imports porte. `Caller` reads
  `TokenInfo.Extra["client_id"]` and `["actor"]`, the keys mcpauth sets.
- **Tools.** Names must be `<Options.Name>_<verb>` in lowercase. Every `ToolSpec` needs a title,
  a description and `Annotations`; all four hints go on the wire, because clients read a
  missing destructive or open-world hint as true. A tool that is not `ReadOnly` needs the write
  scope. tools/list is sorted by name.
- **Errors.** Return `Failure(err, hint)` from a tool: a `not_found` keeps its message plus the
  hint (name the tool that lists valid ids), a bad argument keeps its message, anything else
  becomes `ErrUnavailable`.
- **Confirmations.** A sensitive tool calls `Resume` when `Retrying(req)`, else `Request`.
  `Request` returns an `input_required` result to return as is when the client can elicit;
  otherwise `Outcome.Status` is `pending_confirmation` and `Outcome.ConfirmURL` the link. Put
  `status` and `confirm_url` in the tool's output: mcptest reads those names. `Kind.Fingerprint`
  must change whenever the target changes, so a confirmation never runs on content the person
  did not see. The app's confirm page lists `Open`, shows `State`, and calls `Confirm` or
  `Dismiss`; it must refuse a delegated token, because an agent never confirms its own request.
- **Storage.** `PostgresPending{DB}` over `mcp_actions`; run `PendingSchema` with the app's
  migrations.

```go
confirm, err := mcp.NewConfirmations(mcp.ConfirmConfig{
	Store:      mcp.PostgresPending{DB: sqlDB},
	Key:        stateKey,
	ConfirmURL: "https://agenda.facile.studio/pending",
	Kinds:      map[string]mcp.Kind{"delete_event": {Tool: "agenda_delete_event", Fingerprint: digest, Run: remove}},
})
verifier, err := mcpauth.Verifier(ctx, mcpauth.Config{Issuer: issuer, Resource: resource, ScopePrefix: "agenda", Identities: identities})
err = mcp.Mount(router, mcp.Options{
	Name: "agenda", Title: "Agenda", Version: version, Instructions: instructions,
	Resource: resource, Issuer: issuer, Verifier: verifier, Audit: logger,
	Tools: []mcp.Tool{
		mcp.NewTool(mcp.ToolSpec{Name: "agenda_list_calendars", Title: "List calendars",
			Description: "...", Annotations: &mcp.Annotations{ReadOnly: true, Idempotent: true}}, listCalendars),
	},
})
```

### `mcp/mcptest`

```go
func Run(t *testing.T, cfg Config)
func NewIssuer(t testing.TB) *Issuer
func (i *Issuer) Token(t testing.TB, resource, subject string, scopes ...string) string
func NewPending() *Pending
func Connect(t testing.TB, endpoint, token string, opts ClientOptions) *sdk.ClientSession
```

`Run` checks a live endpoint: 401 with `resource_metadata`, the metadata naming the resource,
an issuer and two scopes, 405 on GET, 403 for a foreign Origin, a stable sorted tool list cached
privately, every tool's prefix, title, hints and object schemas, the 403 `insufficient_scope`
challenge on a write tool with a read-only token, and for each `ConfirmCase`: a link and no run
without elicitation and for a 2025-11-25 client, no run on decline, exactly one run on accept,
no second run on replay, and no run for a link id passed off as a signed state.

## `migrate`

**A separate module.** `go get github.com/FacileStudio/tronc/migrate@v0.1.0` — the git tag is
`migrate/v0.1.0`, but the subdirectory lives in the module path, not in the version query.
Importing `tronc` does not pull it, which is the point: it depends on
`github.com/pressly/goose/v3`, and apps with no database should not carry a migration engine.

```go
const TableName = goose.DefaultTablename // "goose_db_version"

type Config struct {
	DB     *sql.DB      // required; from gormDB.DB() on GORM apps
	FS     fs.FS        // required; rooted at the migrations directory
	Logger *slog.Logger // defaults to slog.Default()
	LockID int64        // defaults to lock.DefaultLockID
}

func Run(ctx context.Context, cfg Config) error
func Command(ctx context.Context, args []string, cfg Config) (handled bool, err error)
```

`Run` applies pending migrations behind a Postgres session advisory lock and is a no-op when
the schema is current. `Command` handles `migrate status|version|up|baseline <n>`, returning
`false` for a normal start so `main` can continue.

| Subcommand | Effect |
|---|---|
| `status` | one line per migration: version, state, applied-at, path. The default |
| `version` | the current schema version as a bare number |
| `up` | apply everything pending — the same path `Run` takes |
| `baseline <n>` | record migrations up to `n` as applied **without running them** |

Three things that are easy to get wrong:

1. **Pass the filesystem through `fs.Sub`.** An `embed.FS` preserves the directory it embedded,
   so handing it over raw means goose looks at the root and finds nothing. A migration set that
   fails to embed would otherwise start cleanly against whatever schema happened to be there,
   which is why `newProvider` refuses a filesystem with no migrations at all.
2. **The pool must allow more than one connection.** The advisory lock pins one for the whole
   run; a pool capped at one deadlocks against itself.
3. **Never call the provider's `Close`.** goose's `Close` closes the underlying `*sql.DB`, which
   belongs to the caller. This package does not expose one.

`baseline` exists because goose has no stamp command of its own, and the documented workaround —
a raw `INSERT` into the ledger — needs a `psql` that a distroless image does not ship. It asks
goose to create the ledger table rather than emitting the DDL itself, because goose writes a
`version_id = 0` sentinel row on creation and every later command fails with
`missing zero version migration` without it. It refuses to stamp a database that already has
history.
