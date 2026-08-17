# tronc — Architecture

How a request moves through the chassis, what each package owns, and the concrete defects in
the hand-rolled copies that `tronc` replaces.

## Runtime topology

`tronc` is a library, so it has no topology of its own. This is the shape of the app that
imports it — one Go binary serving both the API and the client, which is the suite's
one-container rule.

```
Internet ──▶ Traefik ──▶ Go binary (:8080) ──┬──▶ /health, /ready   health.Mount
                                             ├──▶ /api/health, /api/ready
                                             ├──▶ /api/*            your handlers
                                             └──▶ everything else   spa.Handler
                                                                    │
                                                               Postgres 16

docker compose healthcheck ──▶ /app healthcheck ──▶ 127.0.0.1:$PORT/health
```

## The middleware chain

`httpx.NewRouter` applies the standard stack in this order, outermost first:

```
RequestID ──▶ Recoverer ──▶ RealIP ──▶ CORS ──▶ RequestLogger ──▶ your handler
```

All five are tronc's. `RequestID` and `RealIP` replace chi's middleware of the same names:
chi's `RealIP` believes `X-Forwarded-For` from any peer, and chi's `RequestID` takes
`X-Request-Id` verbatim, never echoes it, and mints ids containing the container's hostname.
CORS is applied only when `Config.CORS.AllowedOrigins` is non-empty, so an app with no
configured origins runs one middleware fewer rather than an accept-nothing pass.

`httpx.Chain` applies the identical stack to any `http.Handler`. It exists for the one Go app
in the suite that does not route with chi — Mycelium uses Go 1.22's `http.ServeMux` pattern
matching — so it gets the same behavior without being rewritten onto a router it does not use.

**`Recoverer` sits second, not innermost.** The copies being replaced run it last in the
chain, so a panic raised inside CORS or inside the request logger escapes to `net/http` and is
answered with a bare connection error. Placing it directly under `RequestID` covers the whole
chain while still having a request ID to log with.

## Request lifecycle

1. `RequestID` accepts the caller's `X-Request-Id` when it is well formed — at most
   `MaxRequestIDLength` bytes of alphanumerics and `-_.:/ ` — and mints an opaque one when it is
   not. It echoes the result on the response and attaches it to the context; `middleware.Recoverer`
   and `middleware.RequestLogger` read it back, and a handler reads it with
   `middleware.RequestIDFrom` (or `chimiddleware.GetReqID` — the context key is chi's, so both
   work). The echo is what lets a browser name the id its request was logged under; `CORS` exposes
   the header so a script can actually read it.
2. `Recoverer` arms its `defer`. A recovered panic is logged at error with the request ID,
   method, path, the panic value and a full stack, then answered through
   `httpjson.WriteError` as an `internal` error — the suite envelope, not chi's bare text 500,
   which is the one response in these apps a client cannot parse. `http.ErrAbortHandler` is
   re-panicked, as `net/http` expects.
3. `RealIP` normalizes `RemoteAddr` from the proxy headers.
4. `CORS` answers preflights and decorates cross-origin responses. See below.
5. `RequestLogger` wraps the `ResponseWriter` to capture status and byte count, runs the
   handler, then writes exactly one record.
6. The handler decodes with `httpjson.DecodeJSON`, and answers with `httpjson.WriteJSON` or
   `httpjson.WriteError`.

The logging writer implements `Flush` and `Unwrap`, so streaming responses and
`http.ResponseController` keep working through the middleware.

## The error envelope

Every failure crosses the wire as:

```json
{"error": {"code": "not_found", "message": "space not found"}}
```

`errors.Status` maps a code onto a status, and anything that is not an `*errors.Error` maps to
500. Codes and their statuses are listed in [api.md](api.md#errors).

`httpjson.WriteError` turns anything that is not an `*errors.Error` into a generic `internal`,
so a raw driver error can never leak its text — or your database password — to a client.

## CORS

Origins are matched **exactly** against the browser's `Origin` header. There is no wildcard
subdomain matching and no scheme normalization; see the schemeless-`DOMAIN` trap in
[configuration.md](configuration.md#the-origin-list).

- `middleware.CORS` **panics at construction** when `AllowedOrigins` contains `*` and
  `AllowCredentials` is set. That pair means any site can read authenticated responses, which
  is a credential leak rather than a configuration style. Two apps shipped it.
- An **empty origin list denies everyone**. No localhost defaults leak into production.
- A disallowed origin gets 403 on a preflight, and is passed through undecorated otherwise —
  the browser enforces the rest.
- Preflights answer 204. `Vary` covers `Origin`, `Access-Control-Request-Method` and
  `Access-Control-Request-Headers`.

## Request logging

One record per request, at level `info` for `kind=api`, `debug` for `kind=health` and
`kind=static`, and `error` for any 5xx whatever it was serving.

The quiet default matters because one binary now serves both halves of an app. At `info`,
bundle requests and a healthcheck firing every ten seconds would bury the API traffic in
`docker logs` and ship thousands of asset lines a day into Journal — the shipping handler
forwards whatever the underlying handler admits, and apps run at `info`, so debug records never
leave the host. Set `LOG_LEVEL=debug` to see them again.

Query strings are redacted through `middleware.RedactQuery` against
`middleware.SensitiveQueryKeys` — eleven credential-bearing names. A query that will not parse
is logged as `[unparsable]` rather than raw, because an unparsable query may still carry a
token. In the copies being replaced, one app redacted `?token=`; the rest logged OIDC codes and
bearer tokens in cleartext, and the logs leave the host.

`middleware.ClientIP` reads `Cf-Connecting-Ip`, then the first `X-Forwarded-For` hop, then
`RemoteAddr`. Both headers are client-controlled unless a trusted proxy overwrites them, so
that value is for logging only — never key a rate limiter or an authorization decision on it.
Those key on `RemoteAddr`, which `middleware.RealIP` resolves from the forwarded header only
when the peer is a trusted proxy.

## Why RealIP is ours and not chi's

`NewRouter` installed `chi/middleware.RealIP` until v0.10.0. It rewrites `RemoteAddr` from
`X-Forwarded-For` unconditionally — no peer check — which means any caller able to set a header
gets a new identity on every request, and every per-IP limiter in every app on this chassis was
bypassable. It was measured on Journal, not theorised: 70 requests with a rotating header were
all accepted against a 60/min bucket.

The obvious alternative — trust nothing and key on the raw connection address — is worse here
than it looks. Traefik and each API share a Docker network, so every request arrives from one
private address; a per-IP limiter would collapse into a single global bucket and the login
limiter would lock out the whole world at once. So the default trusts loopback and the private
ranges, which is the deployment as it actually exists, and `TRUSTED_PROXIES` narrows it to a
specific proxy for anyone who wants that.

The residual risk is stated rather than hidden: a peer already inside the private network can
still speak for a visitor. Narrow the list to Traefik's address to close it.

## Health and the SPA catch-all

`health.Mount` registers `/health` and `/ready` at the root **and** under `/api`. The route
lived only at the root while the public edge forwards only `/api/*`, which is why
`/api/health` returned an SPA shell on some apps and a 404 on others. A 200 with an HTML body
is worse than a 404: every external monitor reads it as green.

`/health` touches no dependency and answers `{"status":"ok"}` as soon as the process is
serving. `/ready` runs every registered `health.Check` under a 2 s timeout and answers
`{"status":"ready"}` or 503 `{"status":"not_ready"}`.

`spa.Handler` is mounted last, as the catch-all, and **refuses writes**: anything that is not
GET or HEAD gets 405 with `Allow: GET, HEAD`. This is not tidiness. Journal's collector posts
to an internal URL; once its API moved behind `/api`, that POST fell through to the SPA
catch-all and received **200 with an HTML body**. The shipper treats any 2xx as delivery, so it
discarded every batch — the whole suite's log collection failing silently, with nothing in any
log to show for it. Every deploy probe is a GET, so this is exactly the shape of bug that
survives a deploy check.

The history fallback also does not apply to paths carrying a file extension: a missing bundle
404s instead of receiving `index.html` with a `text/html` content type, which would surface in
the browser as an unrelated MIME or syntax error and hide the real failure. Dotfiles are
refused outright, and containment is delegated to `http.Dir`, so an encoded traversal cannot
escape the directory.

## Distroless healthchecks

Six Facile APIs run `gcr.io/distroless/static-debian12`: no shell, no `wget`, no `curl`, so a
container `HEALTHCHECK` can only work by re-executing the app binary.

```yaml
healthcheck:
  test: ["CMD", "/app", "healthcheck"]
```

`healthcheck.Handle(os.Args)` must be the first thing in `main`. The probe targets `127.0.0.1`
rather than `localhost`: in these containers `localhost` resolves to `::1` first while the
server binds `0.0.0.0`, so a `localhost` probe fails against a perfectly healthy process.

## Why Journal is not a dependency

`tronc` does not import the Journal SDK, on purpose. Journal is itself one of the consumers, so
depending on it would knot the versions; and every app would pull the dependency whether or not
it ships logs. `logger.Config.Wrap` is the seam instead:

```go
log := logger.New(logger.Config{Level: cfg.LogLevel, Wrap: func(h slog.Handler) slog.Handler {
	if cfg.JournalURL == "" {
		return h
	}
	client := journal.New(journal.Config{URL: cfg.JournalURL, Token: cfg.JournalToken})
	return journal.NewHandler(client, h)
}})
```

Four lines stay in your `main.go`, and the dependency stays in your `go.mod`. `env.Core` still
carries `JournalURL` and `JournalToken` so the configuration is shared even though the code
is not.

## What is deliberately not here

No auth, no database layer, no models, no business logic. Auth is a separate package's job;
keeping `tronc` free of any security surface is the whole reason it shipped first, so the
versioning and distribution pipeline got proven on log lines rather than on sessions.
