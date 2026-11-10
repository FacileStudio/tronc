# Rate limiting: replacing the app limiters

Nine rate limiters were written by hand across the suite. Six can become
`middleware.Ratelimit` or `middleware.Limiter` with the config below. Three stay app-side, for
the reasons given at the end.

Read from each repo's `origin/HEAD` on 2026-11-10. Every recipe needs the tronc release that
ships `Limiter`, `MaxKeys`, `OnRefuse` and `RemoteAddrKey64`.

Imports used below:

```go
import (
	"github.com/FacileStudio/tronc/errors"
	"github.com/FacileStudio/tronc/httpjson"
	troncmiddleware "github.com/FacileStudio/tronc/middleware"
)
```

## What tronc's limiter is

A token bucket per key. `Limit` is the refill rate in tokens per second and `Burst` is the
bucket size. A budget of N requests per window W becomes `Limit: N / W.Seconds(), Burst: N`:
the same sustained rate, and the same N at once from a full bucket.

It is not a window counter. Over any span W it can admit up to `Burst + Limit*W`, which is 2N
for the conversion above, against N for a sliding log. If a limiter must hold "at most N in
any W", pick `Burst` and `Limit` so that `Burst + Limit*W <= N`.

Refusals always carry `Retry-After` in whole seconds, computed from the bucket. Apps that sent
a fixed `Retry-After: 60` will send the real wait instead.

The default key is the host of `RemoteAddr`. Every app here builds its router with
`httpx.NewRouter`, whose `RealIP` rewrites `RemoteAddr` from a trusted proxy only, so the
default key is the visitor. Behind Cloudflare, the app also needs `httpx.Config.CDNProxies`
and `CDNHeader`, or every visitor shares the Cloudflare edge's bucket.

Every limiter that faces the public internet sets `MaxKeys: 50000`, the bound Echo already
runs with. Without it the map grows with every new address.

## Comparison

| App limiter | Key | Algorithm | Limits | Refusal | tronc today (v0.20.0) |
|---|---|---|---|---|---|
| Antenne `internal/ratelimit` | client IP from `authcontext.ClientIP` (leftmost `X-Forwarded-For`) | failure counter in a fixed window, then a block | login: 5 failures / 10 min, block 10 min, reset on success. Check-now: 10 calls / min, block 1 min | 429 envelope `rate_limited`; check-now adds computed `Retry-After` | No: failure-only counting and reset; no non-HTTP API |
| Capsule `middleware/ratelimit.go` | raw `RemoteAddr` | fixed window, cleanup goroutine | 30 / min on `POST /pastes` | 429 `{"error":{"code":"rate_limited",...}}` as `text/plain`, `Retry-After` as an HTTP date | Partly: no envelope body, no memory bound |
| Echo `middleware/ratelimit.go` | user id when signed in; else IPv6 /64 and /48, or IPv4 (Cloudflare-aware) | `x/time/rate` token bucket, LRU of 50 000 | per scope: room-create 10 guest / 30 member, join-room 60/120, join 120/240 per min; capture per call | 429 envelope `rate_limited`, `Retry-After: 60` | No: identity-dependent budget, two buckets per IPv6 caller, per-call rate |
| Echo `calllimit/calllimit.go` | call id or room | not a rate limiter: one timer per call, ends it at the plan deadline | `echo.max_call_minutes` | ends the LiveKit room | Not applicable |
| Nuage `middleware/ratelimit.go` | IPv6 /64 or IPv4 (httprate `KeyByIP`) | httprate sliding window counter | 600 / min on `/api/` minus transfer paths | 429 `Too Many Requests` text; `X-RateLimit-*` on every response | Partly: no /64 key |
| Perception `middleware/ratelimit.go` | `troncmiddleware.ClientIP` (spoofable, see below) | token bucket, cleanup goroutine | 120 / min, burst 120, on ingest | 429 envelope `rate_limited`, `Retry-After: 60` | Partly: no envelope body, no memory bound |
| Plume `middleware/ratelimit.go` | host of `RemoteAddr` | token bucket, idle sweep | 30 / min, burst 10, on verify routes | 429 `{"error":{"code":"resource_exhausted",...}}`, `Retry-After: 60` | Partly: no custom body, no memory bound |
| Journal `middleware/browser.go` + `main.go` | IP /64; hashed bearer; hashed browser key + IP; hashed browser key | httprate sliding window counter | session 300/min, ingest 600/min, browser 60/min, browser key ceiling 600/min | 429 envelope `rate_limited`, `Retry-After: 60` | Partly: no envelope body, no /64 key, no memory bound |
| Mycelium `server/auth.go` | host of `RemoteAddr` | sliding log, called inside handlers | login 10/min, device start 20/min, device poll 120/min | 429 envelope `rate_limited` written by the handler | No: needs a non-HTTP `Allow(key)` |
| Bulle `matrixbot/guest_limit.go` | owner id + Matrix sender | sliding log, no HTTP at all | 5 messages / 10 min | a chat reply asking to slow down | No: needs a non-HTTP `Allow(key)` |

The missing pieces, and what each one serves:

| Added | Needed by |
|---|---|
| `Limiter`, `NewLimiter`, `Allow`, `AllowAt` | Mycelium, Bulle, Antenne check-now |
| `RatelimitConfig.OnRefuse` | Capsule, Perception, Plume, Journal (their clients read a JSON body) |
| `RatelimitConfig.MaxKeys` | Capsule, Perception, Plume, Journal, Nuage (all bound memory today) |
| `RemoteAddrKey64` | Nuage, Journal (httprate keyed IPv6 by /64) |

## Recipes

### Capsule: `apps/api/internal/middleware/ratelimit.go`

Delete the file. In `main.go`:

```go
createLimiter := troncmiddleware.Ratelimit(troncmiddleware.RatelimitConfig{
	Limit:   30.0 / 60,
	Burst:   30,
	MaxKeys: 50000,
	OnRefuse: func(w http.ResponseWriter, _ *http.Request) {
		httpjson.WriteError(w, errors.RateLimited("too many requests"))
	},
})
```

and in `modules/pastes/router.go`, `r.With(createLimiter).Post("/", handler.create)`.

Changes a client sees: the body is the same JSON, now as `application/json`. `Retry-After`
becomes seconds instead of a date. The key drops the port, so one visitor no longer gets a
fresh budget per connection when `RealIP` did not rewrite the address.

### Nuage: `apps/api/internal/middleware/ratelimit.go`

Keep `RateLimitAPI` and `IsTransferPath`. Replace the httprate line:

```go
limiter := troncmiddleware.Ratelimit(troncmiddleware.RatelimitConfig{
	Limit:   float64(requests) / window.Seconds(),
	Burst:   requests,
	KeyFunc: troncmiddleware.RemoteAddrKey64,
	MaxKeys: 50000,
})
```

`main.go` keeps `RateLimitAPI(600, time.Minute, apiPrefix+"/", middleware.IsTransferPath)`.
Drop `github.com/go-chi/httprate` with `go mod tidy`.

Changes a client sees: the refusal body becomes `rate limit exceeded`, and the
`X-RateLimit-Limit`, `X-RateLimit-Remaining` and `X-RateLimit-Reset` headers disappear from
every response. Nothing in the Nuage repo reads them.

### Perception: `apps/api/internal/middleware/ratelimit.go`

Delete the file. In `modules/ingest/router.go`:

```go
limiter := troncmiddleware.Ratelimit(troncmiddleware.RatelimitConfig{
	Limit:   ingestPerMinute / ingestWindow.Seconds(),
	Burst:   ingestPerMinute,
	MaxKeys: 50000,
	OnRefuse: func(w http.ResponseWriter, _ *http.Request) {
		httpjson.WriteError(w, errors.RateLimited("too many requests, try again later"))
	},
})
router.With(limiter).Post(PathIngest, ...)
```

Leave `KeyFunc` nil. Today's `RateLimitByClient` keys on `troncmiddleware.ClientIP`, which reads
`Cf-Connecting-Ip` and the leftmost `X-Forwarded-For` as sent. Its own doc comment says it must
never key a rate limiter. Any caller can send a new header per request and get a new bucket.
The default key is `RemoteAddr` after `RealIP`, which is the protection the comment in
Perception believes it already has.

### Plume: `apps/api/internal/middleware/ratelimit.go`

Delete `RateLimiter` and `clientKey`; keep `NoStore`. In `main.go`:

```go
verifyLimiter := troncmiddleware.Ratelimit(troncmiddleware.RatelimitConfig{
	Limit:   30.0 / 60,
	Burst:   10,
	MaxKeys: 50000,
	OnRefuse: func(w http.ResponseWriter, _ *http.Request) {
		httpjson.WriteJSON(w, http.StatusTooManyRequests, map[string]any{
			"error": map[string]string{"code": "resource_exhausted", "message": "too many requests"},
		})
	},
})
verify.RegisterRoutes(api, svc.verify, verifyLimiter)
```

This keeps Plume's body as it is. The suite code for a 429 is `rate_limited`
(`errors.RateLimited`); switching is a separate decision because a client may match on
`resource_exhausted`.

### Journal: four httprate limiters in `apps/api/main.go`

Change the three key functions in `internal/middleware` to `func(*http.Request) string`. Their
error was always nil. `KeyByBrowserKeyAndIP` becomes:

```go
func KeyByBrowserKeyAndIP(request *http.Request) string {
	return authcrypto.HashToken(BrowserKeyToken(request)) + "|" + troncmiddleware.RemoteAddrKey64(request)
}
```

In `main.go`:

```go
refuse := func(w http.ResponseWriter, _ *http.Request) {
	httpjson.WriteError(w, errors.RateLimited("rate limit exceeded"))
}
perMinute := func(n int, key func(*http.Request) string) func(http.Handler) http.Handler {
	return troncmiddleware.Ratelimit(troncmiddleware.RatelimitConfig{
		Limit: float64(n) / 60, Burst: n, KeyFunc: key, MaxKeys: 50000, OnRefuse: refuse,
	})
}
sessionLimiter := perMinute(300, troncmiddleware.RemoteAddrKey64)
ingestLimiter := perMinute(600, middleware.KeyByBearerTokenHash)
browserIngestLimiter := perMinute(60, middleware.KeyByBrowserKeyAndIP)
browserKeyCeiling := perMinute(600, middleware.KeyByBrowserKey)
```

Drop httprate with `go mod tidy`. Update the `KeyByBrowserKeyAndIP` doc comment: it says the IP
comes from chi's `RealIP`, but Journal now builds its router with `httpx.NewRouter`, whose
`RealIP` trusts configured proxies only. The `X-RateLimit-*` headers disappear, as in Nuage.

### Mycelium: `rateLimiter` in `internal/server/auth.go`

Delete `rateLimiter` and `newRateLimiter`. In `server.go`:

```go
logins:    troncmiddleware.NewLimiter(troncmiddleware.RatelimitConfig{Limit: loginMaxAttempts / loginWindow.Seconds(), Burst: loginMaxAttempts, MaxKeys: 50000}),
devStarts: troncmiddleware.NewLimiter(troncmiddleware.RatelimitConfig{Limit: 20.0 / 60, Burst: 20, MaxKeys: 50000}),
devPolls:  troncmiddleware.NewLimiter(troncmiddleware.RatelimitConfig{Limit: 120.0 / 60, Burst: 120, MaxKeys: 50000}),
```

with the fields typed `*troncmiddleware.Limiter`. The three call sites change from
`.allow(clientIP(r), time.Now())` to `.Allow(clientIP(r))`; tests that pass a clock use
`AllowAt(key, now)` and ignore the duration. Bump tronc from v0.17.1.

### Bulle: `apps/api/internal/matrixbot/guest_limit.go`

Keep `slowDown` and the constants. Replace `guestLimiter` with a `*troncmiddleware.Limiter` field
on the manager, built once:

```go
guests: troncmiddleware.NewLimiter(troncmiddleware.RatelimitConfig{
	Limit:   guestBurst / guestWindow.Seconds(),
	Burst:   guestBurst,
	MaxKeys: 50000,
}),
```

In `respond.go`, `m.guests.allow(bot.UserID, msg.Sender, time.Now())` becomes:

```go
allowed, _ := m.guests.AllowAt(strconv.FormatInt(bot.UserID, 10)+"|"+msg.Sender, time.Now())
```

The manager must now be built with the field set: the zero value of `guestLimiter` worked, a
nil `*Limiter` does not. The existing test still holds: five quick messages pass, the sixth a
minute later is refused, and one after the window passes.

### Antenne: the check-now limit only

The check-now button (`internal/server/health.go`) spends on every call, so it is a plain
budget:

```go
checks: troncmiddleware.NewLimiter(troncmiddleware.RatelimitConfig{Limit: checkBudget / checkWindow.Seconds(), Burst: checkBudget}),
```

and in `handleCheckTargetHealth`:

```go
allowed, wait := a.checks.AllowAt(remoteHost(r), time.Now())
if !allowed {
	w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())))
	httpjson.WriteError(w, apierrors.RateLimited("too many checks from this client; wait before checking again"))
	return
}
```

`remoteHost` is the host of `RemoteAddr`. Do not key on `authcontext.ClientIP`: it returns the
leftmost `X-Forwarded-For` entry as the client wrote it, so a caller can mint a fresh key per
request. `checkBlock` goes away; after the burst the bucket admits one check every six seconds
instead of blocking for a minute. The login throttle stays app-side, see below, so
`internal/ratelimit` keeps its file for that one caller.

## Stays app-side

- **Antenne login throttle** (`modules/auth/router.go`, `ThrottleLogins`). It counts failed
  logins only, clears the count on success, and blocks for a fixed time after the fifth
  failure. A token bucket spends on every request and has no reset. Adding "peek without
  spending" and "reset" to tronc for one caller is not worth it. It does need its key fixed:
  `authcontext.ClientIP` trusts the leftmost `X-Forwarded-For`, so the five-failure limit can be
  bypassed by sending a new header with each guess. Use the host of `RemoteAddr`.
- **Echo request limiter** (`internal/middleware/ratelimit.go`). It picks the budget from the
  caller's identity (guest or member), spends an IPv6 caller's /64 and /48 buckets together
  with the /48 at four times the budget, takes the rate per call in `AllowKey`, and resolves the
  visitor through Cloudflare itself. It already runs a bounded LRU over `x/time/rate`. tronc
  would need per-call rates and multi-bucket spends to express it, which no other app uses.
- **Echo call limit** (`internal/calllimit/calllimit.go`). It is not a rate limiter. It arms one
  timer per call and ends the LiveKit room at the plan's deadline. Nothing to replace.
