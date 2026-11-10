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
bucket size. It is not a window counter: over any span of length W it admits at most
`Burst + Limit*W` requests, the full bucket plus what refills during the span.

A budget of N requests per window W is converted one of two ways:

- **Security limiters use a strict bound**: `Burst + Limit*W <= N`, so no span of length W ever
  admits more than N. Pick the burst B (half of N, rounded up, unless the app already has one)
  and set `Limit = (N - B) / W`. For N = 10 per minute: `Burst: 5, Limit: 5.0 / 60`, and
  5 + (5/60)*60 = 10. This applies to Mycelium login and device start, Bulle's guest limit,
  Antenne check-now and Plume's verify routes.
- **Throughput limiters keep the sustained rate**: `Limit: N / W, Burst: N`. A span of length
  W can then admit up to 2N, which is acceptable where the limit shapes load rather than
  guards a secret or a bill. This applies to Capsule pastes, Nuage, Perception ingest, Journal
  and Mycelium device poll.

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

Changes a client sees: the refusal body becomes `rate limit exceeded`, and Nuage stops sending
the `X-RateLimit-Limit`, `X-RateLimit-Remaining` and `X-RateLimit-Reset` headers on every
response. Nothing in the Nuage repo reads them (`git grep -i x-ratelimit origin/HEAD` finds
no match).

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

Leave `KeyFunc` nil. The key must be the host of `RemoteAddr` as rewritten by tronc's real-IP
middleware (`RealIP`, mounted by `httpx.NewRouter`), which is what the default key reads. Never
key on `X-Forwarded-For` read by hand, and never on `troncmiddleware.ClientIP`: it reads
`Cf-Connecting-Ip` and the leftmost `X-Forwarded-For` as the client sent them, so a caller can
send a new header per request and get a new bucket.

### Plume: `apps/api/internal/middleware/ratelimit.go`

Delete `RateLimiter` and `clientKey`; keep `NoStore`. In `main.go`:

```go
verifyLimiter := troncmiddleware.Ratelimit(troncmiddleware.RatelimitConfig{
	Limit:   20.0 / 60,
	Burst:   10,
	MaxKeys: 50000,
	OnRefuse: func(w http.ResponseWriter, _ *http.Request) {
		httpjson.WriteError(w, errors.RateLimited("too many requests"))
	},
})
verify.RegisterRoutes(api, svc.verify, verifyLimiter)
```

Strict bound with N = 30 per minute and Plume's existing burst of 10: 10 + (20/60)*60 = 30.
Today's bucket refills at 30 per minute on top of the burst, so it could admit 40 in a minute.

Changes a client sees: the error code becomes the suite's `rate_limited` instead of
`resource_exhausted`, and `Retry-After` becomes the computed wait instead of 60.

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
`RealIP` trusts configured proxies only.

Changes a client sees: Journal stops sending the `X-RateLimit-Limit`, `X-RateLimit-Remaining`
and `X-RateLimit-Reset` headers. Nothing in the Journal repo reads them
(`git grep -i x-ratelimit origin/HEAD` finds no match). `Retry-After` becomes the computed
wait instead of 60.

### Mycelium: `rateLimiter` in `internal/server/auth.go`

Delete `rateLimiter` and `newRateLimiter`. In `server.go`:

```go
logins:    troncmiddleware.NewLimiter(troncmiddleware.RatelimitConfig{Limit: 5.0 / 60, Burst: 5, MaxKeys: 50000}),
devStarts: troncmiddleware.NewLimiter(troncmiddleware.RatelimitConfig{Limit: 10.0 / 60, Burst: 10, MaxKeys: 50000}),
devPolls:  troncmiddleware.NewLimiter(troncmiddleware.RatelimitConfig{Limit: 120.0 / 60, Burst: 120, MaxKeys: 50000}),
```

with the fields typed `*troncmiddleware.Limiter`. Login and device start are security limiters
and take the strict bound: 5 + (5/60)*60 = 10 per minute and 10 + (10/60)*60 = 20 per minute,
the same caps the sliding log enforced. `loginMaxAttempts` and `loginWindow` can go. Device poll
is a throughput limiter and keeps 120 per minute with a burst of 120. The three call sites change from
`.allow(clientIP(r), time.Now())` to `.Allow(clientIP(r))`; tests that pass a clock use
`AllowAt(key, now)` and ignore the duration. Bump tronc from v0.17.1.

### Bulle: `apps/api/internal/matrixbot/guest_limit.go`

Keep `slowDown` and `guestWindow`; `guestBurst` goes away. Replace `guestLimiter` with a
`*troncmiddleware.Limiter` field on the manager, built once:

```go
guests: troncmiddleware.NewLimiter(troncmiddleware.RatelimitConfig{
	Limit:   2 / guestWindow.Seconds(),
	Burst:   3,
	MaxKeys: 50000,
}),
```

In `respond.go`, `m.guests.allow(bot.UserID, msg.Sender, time.Now())` becomes:

```go
allowed, _ := m.guests.AllowAt(strconv.FormatInt(bot.UserID, 10)+"|"+msg.Sender, time.Now())
```

Strict bound with N = 5 per 10 minutes: 3 + (2/600)*600 = 5. A guest
can send three messages at once, then one every five minutes, and never more than five in any
ten minutes.

The manager must now be built with the field set: the zero value of `guestLimiter` worked, a
nil `*Limiter` does not. `guest_limit_test.go` must change with the bound: today it expects
five quick messages to pass. Under the new config, three quick messages pass, the fourth is
refused, and one sent after the window passes.

### Antenne: the check-now limit only

The check-now button (`internal/server/health.go`) spends on every call, so it is a plain
budget:

```go
checks: troncmiddleware.NewLimiter(troncmiddleware.RatelimitConfig{Limit: 5.0 / 60, Burst: 5, MaxKeys: 50000}),
```

Strict bound with N = 10 per minute: 5 + (5/60)*60 = 10. In `handleCheckTargetHealth`:

```go
allowed, wait := a.checks.AllowAt(remoteHost(r), time.Now())
if !allowed {
	w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())))
	httpjson.WriteError(w, apierrors.RateLimited("too many checks from this client; wait before checking again"))
	return
}
```

`remoteHost` returns the host of `RemoteAddr` as rewritten by tronc's real-IP middleware
(`RealIP`, mounted by `httpx.NewRouter`). The key must be that, never `X-Forwarded-For` read by
hand and never a `ClientIP` helper (`authcontext.ClientIP` or `troncmiddleware.ClientIP`): both
return a header the client wrote, so a caller can mint a fresh key per request. `checkBudget`,
`checkWindow` and `checkBlock` go away; after the burst of five the bucket admits one check
every twelve seconds instead of blocking for a minute. The login throttle stays app-side, see below, so
`internal/ratelimit` keeps its file for that one caller.

## Stays app-side

- **Antenne login throttle** (`modules/auth/router.go`, `ThrottleLogins`). It counts failed
  logins only, clears the count on success, and blocks for a fixed time after the fifth
  failure. A token bucket spends on every request and has no reset. Adding "peek without
  spending" and "reset" to tronc for one caller is not worth it. Its key must be the host of
  `RemoteAddr` as rewritten by tronc's real-IP middleware, never `X-Forwarded-For` read by hand
  and never a `ClientIP` helper. Today it uses `authcontext.ClientIP`, which trusts the leftmost
  `X-Forwarded-For`, so the five-failure limit can be bypassed by sending a new header with each
  guess.
- **Echo request limiter** (`internal/middleware/ratelimit.go`). It picks the budget from the
  caller's identity (guest or member), spends an IPv6 caller's /64 and /48 buckets together
  with the /48 at four times the budget, takes the rate per call in `AllowKey`, and resolves the
  visitor through Cloudflare itself. It already runs a bounded LRU over `x/time/rate`. tronc
  would need per-call rates and multi-bucket spends to express it, which no other app uses.
- **Echo call limit** (`internal/calllimit/calllimit.go`). It is not a rate limiter. It arms one
  timer per call and ends the LiveKit room at the plan's deadline. Nothing to replace.
