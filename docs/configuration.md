# tronc — Configuration

Every environment variable the `tronc` source reads, what it defaults to, and the traps in
adopting `env.LoadCore` in an app that already has its own configuration.

`tronc` reads nothing at import time. Variables are read only when you call `env.LoadCore`,
`env.LoadCoreWithout`, one of the typed helpers, `spa.DirFromEnv`, or `healthcheck.Handle`.

## `env.LoadCore`

| Variable | Required | Default | What it does |
|---|---|---|---|
| `DATABASE_URL` | yes | — | Postgres connection string. `LoadCore` returns an error when unset |
| `PORT` | no | `8080` | HTTP listen port. Must parse as an integer |
| `LOG_LEVEL` | no | `info` | `debug` / `info` / `warn` / `error`. Anything else means `info` |
| `APP_ENV` | no | `development` | `development` / `staging` / `production` |
| `CORS_ALLOWED_ORIGINS` | no | *(none — deny)* | Comma-separated allowed origins |
| `JOURNAL_URL` | no | *(none)* | Read into `Core.JournalURL`; wired by the app, not by tronc |
| `JOURNAL_TOKEN` | no | *(none)* | Read into `Core.JournalToken` |

`env.LoadCoreWithout` is the same call with `DATABASE_URL` read if present but not required.
It exists for a service that has no database of its own — Jardin keeps its state as files, and
requiring a database URL kept it off the shared configuration entirely.

`APP_ENV` is parsed leniently: `prod` and `production` both mean production, `stage` and
`staging` both mean staging, and anything unrecognized — including an empty value and
`banana` — is development. It **never gates security behavior**. CORS is decided by the
configured origin list alone, so a missing `APP_ENV` cannot open an app up.

## Read elsewhere in the module

| Variable | Read by | Default | What it does |
|---|---|---|---|
| `CLIENT_DIR` | `spa.DirFromEnv` | `./client` | Directory holding the built client |
| `PORT` | `healthcheck.Handle` | `8080` | Port the self-probe targets on `127.0.0.1` |

Note that `healthcheck` reads `PORT` with `os.Getenv` directly rather than through `env`, and
so applies its own `8080` fallback. The two defaults agree; if your app overrides the listen
port in code without setting `PORT`, the container self-probe will hit the wrong one.

## The origin list

`env.CORSOrigins` reads the **first** name in `env.CORSOriginKeys` that is set to a non-blank
value and splits it on commas:

```
CORS_ALLOWED_ORIGINS  →  ALLOWED_ORIGINS  →  DOMAINS  →  DOMAIN
                      →  CORS_ORIGINS  →  TRUSTED_ORIGINS  →  CLIENT_ORIGIN
```

`CORS_ALLOWED_ORIGINS` is the canonical name, from `GoSvelteBoilerplate`. The other six are
the drift that leaked out of it — Agenda, Courrier and Sablier read `DOMAINS`; Plume and
Vision read the singular `DOMAIN`. Reading all seven is what lets a repo adopt `tronc` without
its deployment config changing in the same breath. Without the fallbacks, adoption would have
silently emptied the allowed-origin list and denied every cross-origin caller.

**Trap: `DOMAIN` is often set to a bare hostname.** `CORSOrigins` does no normalization — it
splits on commas and trims, nothing more — and `middleware.CORS` matches origins **exactly**
against the browser's `Origin` header, which always carries a scheme. So a deployment with
`DOMAIN=agenda.example.com` and no `CORS_ALLOWED_ORIGINS` produces an origin list that can
never match anything, and every cross-origin request is denied. Set `CORS_ALLOWED_ORIGINS`
with the scheme included. Agenda works around the same problem for its OIDC redirect target by
scanning the list for the first entry that starts with `http://` or `https://`.

**Trap: `*` with credentials is fatal, by design.** `middleware.CORS` panics at construction
when `AllowedOrigins` contains `*` and `AllowCredentials` is true. It fails at startup rather
than at request time, so this shows up on the first deploy, not the first login.

## Typed helpers

Apps read their own variables with these rather than `os.Getenv`, so parsing and blank-value
handling stay consistent. Every helper trims whitespace and treats a blank value as unset.

| Helper | Signature | Behavior |
|---|---|---|
| `String` | `String(key, fallback string) string` | Returns `fallback` when unset or blank |
| `Required` | `Required(key string) (string, error)` | Errors with `env: <key> is required` |
| `Int` | `Int(key string, fallback int) (int, error)` | `strconv.Atoi`; errors on garbage |
| `Bool` | `Bool(key string, fallback bool) (bool, error)` | `strconv.ParseBool` spellings only |
| `Duration` | `Duration(key string, fallback time.Duration) (time.Duration, error)` | `time.ParseDuration`, e.g. `30s`, `5m` |
| `List` | `List(key string) []string` | Comma-separated, blanks dropped, `nil` when empty |

**Trap: `Bool` fails on anything `strconv.ParseBool` will not take.** `true`, `false`, `1`,
`0`, `t`, `f`, `T`, `F`, `TRUE`, `FALSE`, `True`, `False` are accepted; `yes`, `no`, `on` and
`off` are **not** — they return an error rather than falling back. The same applies to `Int`
and `Duration`. Since apps typically bubble that error out of `Load` and exit 1, a variable
spelled `SSO_ONLY=yes` takes the process down at boot instead of quietly meaning `false`.
That is the intended behavior: a misconfigured security flag should never be guessed at.

**A missing required variable exits 1.** `LoadCore` and `Required` only return errors; the
`os.Exit(1)` is the caller's, and every app in the suite does it. Expect a container to
crash-loop rather than boot half-configured.

## Adopting `LoadCore` in an existing app

Two things change under an app that previously rolled its own configuration.

**The `PORT` default moved from 4000 to 8080.** The Go apps historically defaulted to `4000`;
`env.LoadCore` defaults to `8080`. If the deployment never set `PORT` explicitly — relying on
the app's own default — switching to `LoadCore` moves the listen port and the Traefik router
stops finding a backend. Set `PORT` explicitly, or keep your own default and fill `Core.Port`
yourself. Agenda does the latter, and still defaults to `4000`.

**`LoadCore` is unusable when the DSN is assembled from `DB_*` variables.** It requires
`DATABASE_URL` and there is no hook to substitute another source. An app whose deployment only
sets `DB_USER`, `DB_PASSWORD`, `DB_HOST`, `DB_PORT`, `DB_NAME` and `DB_SSLMODE` must build the
URL itself and populate `env.Core` field by field, using the typed helpers for each field.
Agenda's `internal/env` is the reference for that shape. `LoadCoreWithout` does not solve this
case either — it makes the database optional, not sourced from somewhere else.

Consumers conventionally alias the import as `troncenv` and embed `troncenv.Core`:

```go
import troncenv "github.com/FacileStudio/tronc/env"

type Config struct {
	troncenv.Core
	StorageDir string
	SSOOnly    bool
}
```
