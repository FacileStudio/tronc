package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestRatelimitAllowsUnderTheBurstThenRefuses(t *testing.T) {
	handler := Ratelimit(RatelimitConfig{Limit: 1, Burst: 3})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := range 3 {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.RemoteAddr = "203.0.113.7:1000"
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200", i+1, recorder.Code)
		}
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "203.0.113.7:1000"
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", recorder.Code)
	}
	if body := recorder.Body.String(); body != RateLimitExceededBody {
		t.Errorf("body = %q, want %q", body, RateLimitExceededBody)
	}
	retryAfter := recorder.Header().Get("Retry-After")
	if seconds, err := strconv.Atoi(retryAfter); err != nil || seconds < 1 {
		t.Errorf("Retry-After = %q, want an integer of at least one second", retryAfter)
	}
}

func TestRatelimitKeysAreIsolated(t *testing.T) {
	handler := Ratelimit(RatelimitConfig{
		Limit: 1,
		Burst: 1,
		KeyFunc: func(request *http.Request) string {
			return request.Header.Get("X-Tenant")
		},
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	first := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-Tenant", "sablier")
	handler.ServeHTTP(first, request)

	spent := httptest.NewRecorder()
	handler.ServeHTTP(spent, request)
	if spent.Code != http.StatusTooManyRequests {
		t.Fatalf("second request from same key status = %d, want 429", spent.Code)
	}

	other := httptest.NewRecorder()
	neighbor := httptest.NewRequest(http.MethodGet, "/", nil)
	neighbor.Header.Set("X-Tenant", "nuage")
	handler.ServeHTTP(other, neighbor)
	if other.Code != http.StatusOK {
		t.Errorf("fresh key status = %d, want 200", other.Code)
	}
}

func TestRatelimitDefaultsToRemoteAddrHost(t *testing.T) {
	handler := Ratelimit(RatelimitConfig{Limit: 1, Burst: 1})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	spend := func(addr string) int {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.RemoteAddr = addr
		handler.ServeHTTP(recorder, request)
		return recorder.Code
	}

	if got := spend("192.0.2.10:40001"); got != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", got)
	}
	if got := spend("192.0.2.10:40002"); got != http.StatusTooManyRequests {
		t.Errorf("same address, new port status = %d, want 429", got)
	}
	if got := spend("192.0.2.11:40003"); got != http.StatusOK {
		t.Errorf("different address status = %d, want 200", got)
	}
}

func TestRatelimitRefillsContinuously(t *testing.T) {
	handler := Ratelimit(RatelimitConfig{Limit: 100, Burst: 1})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	exhaust := func() int {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.RemoteAddr = "203.0.113.9:2000"
		handler.ServeHTTP(recorder, request)
		return recorder.Code
	}

	if got := exhaust(); got != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", got)
	}
	if got := exhaust(); got != http.StatusTooManyRequests {
		t.Fatalf("immediate retry status = %d, want 429", got)
	}

	time.Sleep(50 * time.Millisecond)
	if got := exhaust(); got != http.StatusOK {
		t.Errorf("after refill status = %d, want 200", got)
	}
}

func TestRatelimitHonorsCustomStatusCode(t *testing.T) {
	handler := Ratelimit(RatelimitConfig{Limit: 1, Burst: 0, StatusCode: http.StatusServiceUnavailable})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "203.0.113.20:3000"
	handler.ServeHTTP(httptest.NewRecorder(), request)

	refused := httptest.NewRecorder()
	handler.ServeHTTP(refused, request)
	if refused.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", refused.Code)
	}
}

func TestRatelimitZeroConfigStillWorks(t *testing.T) {
	handler := Ratelimit(RatelimitConfig{})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := range DefaultRatelimitLimit {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.RemoteAddr = "198.51.100.5:5000"
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("request %d of the default burst status = %d, want 200", i+1, recorder.Code)
		}
	}

	refused := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "198.51.100.5:5000"
	handler.ServeHTTP(refused, request)
	if refused.Code != http.StatusTooManyRequests {
		t.Errorf("beyond the derived burst status = %d, want 429", refused.Code)
	}
}
