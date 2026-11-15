package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFixedWindowRefusesPastBudgetAndRecovers(t *testing.T) {
	state := &fixedWindow{entries: map[string]*fixedWindowEntry{}, requests: 2, window: time.Minute}
	start := time.Now()
	for range 2 {
		if ok, _ := state.take("a", start); !ok {
			t.Fatal("budget must admit its full size at once")
		}
	}
	ok, retry := state.take("a", start.Add(10*time.Second))
	if ok || retry != 51 {
		t.Fatalf("third request: ok=%v retry=%d, want refusal and Retry-After 51", ok, retry)
	}
	if ok, _ := state.take("b", start); !ok {
		t.Fatal("another key must have its own budget")
	}
	if ok, _ := state.take("a", start.Add(61*time.Second)); !ok {
		t.Fatal("a new window must restore the budget")
	}
}

func TestFixedWindowMaxKeysStartsOver(t *testing.T) {
	state := &fixedWindow{entries: map[string]*fixedWindowEntry{}, requests: 1, window: time.Hour, maxKeys: 2}
	now := time.Now()
	state.take("a", now)
	state.take("b", now)
	state.take("c", now)
	if len(state.entries) > 2 {
		t.Fatalf("map grew past MaxKeys: %d", len(state.entries))
	}
}

func TestFixedWindowMiddlewareEnvelope(t *testing.T) {
	handler := FixedWindow(FixedWindowConfig{Requests: 1})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "192.0.2.1:1234"
	handler.ServeHTTP(httptest.NewRecorder(), request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusTooManyRequests || recorder.Header().Get("Retry-After") == "" ||
		!strings.Contains(recorder.Body.String(), `"rate_limited"`) {
		t.Fatalf("refusal wrong: %d %q %v", recorder.Code, recorder.Body.String(), recorder.Header())
	}
}
