# tronc

The Go app chassis for the [Facile Studio](https://facile.studio) suite — the HTTP plumbing
every Facile API needs and none of them should be re-deciding.

Twelve Go services currently carry their own copy of this code under `apps/api/internal/`.
The copies have drifted: five variants of the error envelope, four of the request logger,
five of the CORS middleware. `tronc` is the single version, tagged and versioned.

```sh
go get github.com/FacileStudio/tronc
```

Requires **Go 1.24**. Depends on `github.com/go-chi/chi/v5` and nothing else — deliberately.

## What it gives you

```go
package main

import (
	"log/slog"
	"net/http"
	"os"

	"github.com/FacileStudio/tronc/env"
	"github.com/FacileStudio/tronc/health"
	"github.com/FacileStudio/tronc/healthcheck"
	"github.com/FacileStudio/tronc/httpx"
	"github.com/FacileStudio/tronc/logger"
	"github.com/FacileStudio/tronc/middleware"
)

func main() {
	if healthcheck.Handle(os.Args) {
		return
	}

	cfg, err := env.LoadCore()
	if err != nil {
		slog.Error("config", slog.Any("error", err))
		os.Exit(1)
	}

	log := logger.New(logger.Config{Level: cfg.LogLevel})

	router := httpx.NewRouter(httpx.Config{
		Logger: log,
		CORS: middleware.CORSConfig{
			AllowedOrigins:   cfg.CORSAllowedOrigins,
			AllowCredentials: true,
		},
	})
	health.Mount(router, health.DB(db))

	// ... your routes

	_ = http.ListenAndServe(":"+strconv.Itoa(cfg.Port), router)
}
```

| Package | What it is |
|---|---|
| `errors` | the error envelope: a code, a message, a wrapped cause, and one code→status map |
| `httpjson` | `DecodeJSON` (1 MB cap, unknown fields rejected, single object enforced), `DecodeGzipJSON` (bomb-safe), `WriteJSON`, `WriteError` |
| `logger` | slog JSON on stdout, level from config, and a `Wrap` seam for log shipping |
| `middleware` | `RequestLogger` (classifies api / health / static and logs each at a matching level), `CORS`, `Recoverer` |
| `httpx` | `NewRouter` — the standard chi chain, assembled once; `Chain` applies the same stack to any handler |
| `health` | `/health` and `/ready`, mounted at both the root and `/api` |
| `healthcheck` | a `healthcheck` argv hook so a distroless container can probe itself |
| `spa` | serves a built SPA from a directory, with a history fallback that will not mask a missing bundle |
| `env` | the shared configuration, plus typed `String`/`Int`/`Bool`/`Duration`/`List` helpers |

## The error envelope

Every failure crosses the wire as:

```json
{"error": {"code": "not_found", "message": "space not found"}}
```

| Code | Status |
|---|---|
| `invalid_argument` | 400 |
| `unauthenticated` | 401 |
| `permission_denied` | 403 |
| `not_found` | 404 |
| `already_exists` | 409 |
| `failed_precondition` | 412 |
| `resource_exhausted` | 413 |
| `rate_limited` | 429 |
| `internal` | 500 |

`httpjson.WriteError` turns anything that is not an `*errors.Error` into a generic
`internal`, so a raw driver error can never leak its text — or your database password — to
a client.

## Things it fixes on the way in

These are not refactors. Each one is a live defect in the copies being replaced.

- **CORS refuses to start on `*` + credentials.** Two apps today accept `*` as an allowed
  origin *and* send `Access-Control-Allow-Credentials: true`, which lets any website read
  their authenticated responses. `middleware.CORS` panics on that pair at construction.
- **An empty origin list denies everyone.** No localhost defaults leaking into production.
- **`Recoverer` moves out of innermost position.** The apps run it last in the chain, so a
  panic in CORS or in the request logger escapes to `net/http`. `httpx.NewRouter` puts it
  directly under `RequestID`, covering the whole chain while still having an ID to log.
- **A recovered panic answers in the envelope.** chi's `Recoverer` writes a bare text 500 —
  the one response in these apps a client cannot parse.
- **Query strings are redacted everywhere.** One app redacts `?token=`; the rest log OIDC
  codes and bearer tokens in cleartext, and the logs leave the host. See
  `middleware.SensitiveQueryKeys`.
- **`/health` answers at `/api/health` too.** The route lives at the root while the public
  edge forwards only `/api/*`, which is why `/api/health` returns an SPA shell on some apps
  and a 404 on others. A 200 with an HTML body is worse than a 404 — every external monitor
  reads it as green.
- **Distroless containers can have a healthcheck at all.** No shell, no `curl`, so the only
  route is re-executing the app binary: `test: ["CMD", "/app", "healthcheck"]`.

## Configuration

`env.LoadCore` reads what every app shares. `DATABASE_URL` is required; the rest default.

| Variable | Default | Notes |
|---|---|---|
| `APP_ENV` | `development` | `development` / `staging` / `production`. Never gates security behaviour |
| `PORT` | `8080` | |
| `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `DATABASE_URL` | — | required |
| `CORS_ALLOWED_ORIGINS` | *(none — deny)* | comma-separated |
| `JOURNAL_URL`, `JOURNAL_TOKEN` | *(none)* | read here, wired by the app |

`CORS_ALLOWED_ORIGINS` falls back to `ALLOWED_ORIGINS`, `DOMAINS`, `DOMAIN`, `CORS_ORIGINS`,
`TRUSTED_ORIGINS` and `CLIENT_ORIGIN`, in that order — the six names that drifted out of the
boilerplate. A repo can adopt `tronc` without its deployment config changing in the same breath.

## Why Journal is not in here

`tronc` does not import the Journal SDK, on purpose. Journal is itself one of the consumers,
so depending on it would knot the versions; and every app would pull the dependency whether
or not it ships logs. Instead `logger.Config.Wrap` is a seam:

```go
log := logger.New(logger.Config{Level: cfg.LogLevel, Wrap: func(h slog.Handler) slog.Handler {
	if cfg.JournalURL == "" {
		return h
	}
	client := journal.New(journal.Config{URL: cfg.JournalURL, Token: cfg.JournalToken})
	return journal.NewHandler(client, h)
}})
```

Four lines stay in your `main.go`, and the dependency stays in your `go.mod`.

## What is deliberately not here

No auth, no database layer, no models, no business logic. Auth is a separate package's job;
keeping `tronc` free of any security surface is the whole reason it ships first, so the
versioning and distribution pipeline gets proven on log lines rather than on sessions.

## Development

```sh
mise run check      # gofmt + vet + test + golangci-lint
mise run test
mise run format
mise run hooks      # enable the tracked pre-push gate in this clone
```

The pre-push hook calls `scripts/check.sh` directly rather than through mise, because
`mise run` resolves every tool in the merged config before running any task body — one
broken tool anywhere in a global config would otherwise take the gate down.

## Versioning

Semver tags, never branch tracking:

```
require github.com/FacileStudio/tronc v0.7.0
```

Breaking changes bump the minor while `v0`. See [CHANGELOG.md](CHANGELOG.md).
