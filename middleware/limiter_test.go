package middleware

import (
	"math"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func TestRatelimitZeroValueOfNewFieldsKeepsTheRefusalByteForByte(t *testing.T) {
	handler := Ratelimit(RatelimitConfig{Limit: 1, Burst: 1, MaxKeys: 0, OnRefuse: nil})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	serve := func() *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.RemoteAddr = "203.0.113.30:4000"
		handler.ServeHTTP(recorder, request)
		return recorder
	}

	admitted := serve()
	if admitted.Code != http.StatusOK || admitted.Body.String() != "ok" || admitted.Header().Get("Retry-After") != "" {
		t.Fatalf("admitted response = %d %q %v, want 200 \"ok\" without Retry-After", admitted.Code, admitted.Body.String(), admitted.Header())
	}

	refused := serve()
	wantHeader := http.Header{
		"Content-Type": {"text/plain; charset=utf-8"},
		"Retry-After":  {"1"},
	}
	if refused.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", refused.Code)
	}
	if !reflect.DeepEqual(refused.Header(), wantHeader) {
		t.Errorf("headers = %v, want exactly %v", refused.Header(), wantHeader)
	}
	if body := refused.Body.String(); body != "rate limit exceeded" {
		t.Errorf("body = %q, want %q", body, "rate limit exceeded")
	}
}

type originalBucket struct {
	tokens float64
	last   time.Time
}

type originalLimiter struct {
	limit   float64
	burst   float64
	buckets map[string]*originalBucket
}

func (l originalLimiter) allow(key string, now time.Time) (bool, int) {
	current, ok := l.buckets[key]
	if !ok {
		current = &originalBucket{tokens: l.burst, last: now}
		l.buckets[key] = current
	}
	current.tokens = math.Min(l.burst, current.tokens+now.Sub(current.last).Seconds()*l.limit)
	current.last = now
	if current.tokens >= 1 {
		current.tokens--
		return true, 0
	}
	wait := int(math.Ceil((1 - current.tokens) / l.limit))
	return false, max(wait, 1)
}

func TestLimiterMatchesTheOriginalBucketDecisionForDecision(t *testing.T) {
	reference := originalLimiter{limit: 0.7, burst: 3, buckets: map[string]*originalBucket{}}
	limiter := NewLimiter(RatelimitConfig{Limit: 0.7, Burst: 3})
	random := rand.New(rand.NewPCG(7, 11))
	keys := []string{"a", "b", "c", "d", "e"}
	now := time.Unix(1_700_000_000, 0)
	for step := range 20000 {
		now = now.Add(time.Duration(random.IntN(1500)) * time.Millisecond)
		key := keys[random.IntN(len(keys))]
		wantAllowed, wantWait := reference.allow(key, now)
		gotAllowed, gotWait := limiter.take(key, now)
		if gotAllowed != wantAllowed || gotWait != wantWait {
			t.Fatalf("step %d key %s = (%v, %d), want (%v, %d)", step, key, gotAllowed, gotWait, wantAllowed, wantWait)
		}
	}
}

func TestLimiterWithoutMaxKeysForgetsNothing(t *testing.T) {
	limiter := NewLimiter(RatelimitConfig{Limit: 0.001, Burst: 1})
	now := time.Unix(1_700_000_000, 0)
	limiter.take("first", now)
	for i := range 10000 {
		limiter.take("key-"+strconv.Itoa(i), now)
	}
	if allowed, _ := limiter.take("first", now); allowed {
		t.Fatal("a spent key was forgotten without MaxKeys")
	}
	if held := len(limiter.current) + len(limiter.previous); held != 10001 {
		t.Errorf("held %d keys, want 10001", held)
	}
}

func TestLimiterMaxKeysForgetsTheIdleKeysFirstAndStaysBounded(t *testing.T) {
	limiter := NewLimiter(RatelimitConfig{Limit: 0.001, Burst: 1, MaxKeys: 4})
	now := time.Unix(1_700_000_000, 0)
	for _, key := range []string{"a", "b", "c", "a", "d"} {
		limiter.take(key, now)
	}
	if allowed, _ := limiter.take("b", now); !allowed {
		t.Error("the idle key b was kept, want it forgotten and refilled")
	}
	if allowed, _ := limiter.take("a", now); allowed {
		t.Error("the recently used key a was forgotten, want it kept and spent")
	}

	for i := range 1000 {
		limiter.take("flood-"+strconv.Itoa(i), now)
		if held := len(limiter.current) + len(limiter.previous); held > 4 {
			t.Fatalf("held %d keys after %d inserts, want at most 4", held, i+1)
		}
	}
}

func TestRatelimitOnRefuseWritesTheRefusal(t *testing.T) {
	var called int
	handler := Ratelimit(RatelimitConfig{Limit: 1, Burst: 1, StatusCode: http.StatusServiceUnavailable,
		OnRefuse: func(w http.ResponseWriter, _ *http.Request) {
			called++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":"rate_limited","message":"slow down"}}`))
		},
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "203.0.113.40:4000"
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if called != 0 {
		t.Fatalf("OnRefuse ran on an admitted request")
	}

	refused := httptest.NewRecorder()
	handler.ServeHTTP(refused, request)
	if called != 1 || refused.Code != http.StatusTooManyRequests {
		t.Errorf("OnRefuse calls = %d, status = %d, want 1 and 429", called, refused.Code)
	}
	if refused.Header().Get("Retry-After") != "1" {
		t.Errorf("Retry-After = %q, want it set before OnRefuse runs", refused.Header().Get("Retry-After"))
	}
	if body := refused.Body.String(); body != `{"error":{"code":"rate_limited","message":"slow down"}}` {
		t.Errorf("body = %q, want the OnRefuse body alone", body)
	}
}

func TestLimiterAllowAtReportsTheWaitAndRefills(t *testing.T) {
	limiter := NewLimiter(RatelimitConfig{Limit: 0.5, Burst: 1})
	now := time.Unix(1_700_000_000, 0)

	if allowed, wait := limiter.AllowAt("bot|alice", now); !allowed || wait != 0 {
		t.Fatalf("first call = (%v, %v), want (true, 0)", allowed, wait)
	}
	if allowed, wait := limiter.AllowAt("bot|alice", now); allowed || wait != 2*time.Second {
		t.Errorf("immediate retry = (%v, %v), want (false, 2s)", allowed, wait)
	}
	if allowed, _ := limiter.AllowAt("bot|bob", now); !allowed {
		t.Error("another key was refused, want keys isolated")
	}
	if allowed, _ := limiter.AllowAt("bot|alice", now.Add(2*time.Second)); !allowed {
		t.Error("after the reported wait the call was refused")
	}

	live := NewLimiter(RatelimitConfig{Limit: 0.001, Burst: 2})
	got := []bool{live.Allow("k"), live.Allow("k"), live.Allow("k")}
	if !reflect.DeepEqual(got, []bool{true, true, false}) {
		t.Errorf("Allow over a burst of two = %v, want [true true false]", got)
	}
}

func TestRemoteAddrKey64(t *testing.T) {
	cases := map[string]string{
		"203.0.113.7:1000":                   "203.0.113.7",
		"[2001:db8:1:2:aaaa::1]:443":         "2001:db8:1:2::/64",
		"[2001:db8:1:2:ffff:ffff:ffff:9]:80": "2001:db8:1:2::/64",
		"[2001:db8:1:3::1]:80":               "2001:db8:1:3::/64",
		"[::ffff:198.51.100.4]:80":           "198.51.100.4",
		"[fe80::1%eth0]:80":                  "fe80::/64",
		"pipe":                               "pipe",
	}
	for remote, want := range cases {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.RemoteAddr = remote
		if got := RemoteAddrKey64(request); got != want {
			t.Errorf("RemoteAddrKey64(%q) = %q, want %q", remote, got, want)
		}
	}
}
