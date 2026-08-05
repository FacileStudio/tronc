# tronc — API

The complete exported surface, package by package. Nine packages, all importable under
`github.com/FacileStudio/tronc/<package>`. Behavior and rationale live in
[architecture.md](architecture.md); this page is the reference.

## `errors`

The suite error envelope: a stable machine-readable code, a human-readable message, and the
wrapped cause.

```go
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Cause   error  `json:"-"`
}

func (e *Error) Error() string   // returns Message
func (e *Error) Unwrap() error   // returns Cause, so errors.Is/As reach it
```

`Cause` is `json:"-"`, so it never crosses the wire.

| Constructor | Code | Status |
|---|---|---|
| `Invalid(message string) *Error` | `invalid_argument` | 400 |
| `Unauthorized(message string) *Error` | `unauthenticated` | 401 |
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
status the code maps to, and turns anything that is not an `*errors.Error` into a generic
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
| `AllowedHeaders` | `[]string` | `DefaultAllowedHeaders` | `Accept`, `Authorization`, `Content-Type` |
| `AllowedMethods` | `[]string` | `DefaultAllowedMethods` | `GET`, `POST`, `PUT`, `PATCH`, `DELETE`, `OPTIONS` |
| `MaxAgeSeconds` | `int` | `600` | `Access-Control-Max-Age` |

`CORS` **panics at construction** when `AllowedOrigins` contains `*` and `AllowCredentials` is
set. Apps with a custom header — Capsule's `X-Delete-Token` — extend `AllowedHeaders`.

### Request logging

| Symbol | Signature |
|---|---|
| `RequestLogger` | `RequestLogger(logger *slog.Logger) func(http.Handler) http.Handler` |
| `RequestLoggerWith` | `RequestLoggerWith(logger *slog.Logger, cfg RequestLoggerConfig) func(http.Handler) http.Handler` |
| `RequestLoggerConfig` | `{ APIPrefix string; QuietLevel slog.Level }`, defaulting to `/api` and `slog.LevelDebug` |
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
rate limiter or an authorization decision on this value — use `RemoteAddr` for those.

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
	Logger *slog.Logger            // defaults to slog.Default()
	CORS   middleware.CORSConfig   // applied only when AllowedOrigins is non-empty
}

func NewRouter(cfg Config) *chi.Mux
func Chain(cfg Config, next http.Handler) http.Handler
```

Both apply `RequestID → Recoverer → RealIP → CORS → RequestLogger`, outermost first.
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
```

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
