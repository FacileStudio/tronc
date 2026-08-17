package middleware

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// serveWithRequestID runs one request through the middleware and reports the id
// the handler saw alongside the response.
func serveWithRequestID(t *testing.T, header string) (seen string, recorder *httptest.ResponseRecorder) {
	t.Helper()
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestIDFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	request := httptest.NewRequest(http.MethodGet, "/api/things", nil)
	if header != "" {
		request.Header.Set(RequestIDHeader, header)
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return seen, recorder
}

// The echo is the point: a page cannot correlate its own failure with the
// server's logs unless the response names the id the request was logged under.
func TestRequestIDIsEchoed(t *testing.T) {
	seen, recorder := serveWithRequestID(t, "")

	if seen == "" {
		t.Fatal("handler saw no request id")
	}
	if got := recorder.Header().Get(RequestIDHeader); got != seen {
		t.Fatalf("echoed %q, want the id the handler saw, %q", got, seen)
	}
}

// A caller's id is accepted so the browser SDK can name the id before the
// request leaves, which is what ties a failed fetch to the handler behind it.
func TestRequestIDAcceptsAWellFormedCaller(t *testing.T) {
	const sent = "0d5f6f4e-9d2a-4a9b-8d1e-7c6b5a4f3e2d"

	seen, recorder := serveWithRequestID(t, sent)

	if seen != sent {
		t.Fatalf("handler saw %q, want the caller's %q", seen, sent)
	}
	if got := recorder.Header().Get(RequestIDHeader); got != sent {
		t.Fatalf("echoed %q, want %q", got, sent)
	}
}

// The value lands in every log line the request produces and in Journal's
// meta.request_id, so what a caller may put there is bounded on both axes.
func TestRequestIDRefusesWhatItCannotLog(t *testing.T) {
	cases := map[string]string{
		"too long":       strings.Repeat("a", MaxRequestIDLength+1),
		"newline":        "abc\ndef",
		"carriage":       "abc\rdef",
		"space":          "abc def",
		"quote":          `abc"def`,
		"non ascii":      "abcé",
		"null byte":      "abc\x00def",
		"header comment": "abc;def",
	}

	for name, sent := range cases {
		t.Run(name, func(t *testing.T) {
			seen, recorder := serveWithRequestID(t, sent)

			if seen == sent {
				t.Fatalf("accepted %q, want a freshly minted id", sent)
			}
			if seen == "" {
				t.Fatal("refused the caller's id without minting one")
			}
			if got := recorder.Header().Get(RequestIDHeader); got != seen {
				t.Fatalf("echoed %q, want the minted %q", got, seen)
			}
		})
	}
}

// Exactly at the limit is legal — the bound is a cap, not a hint.
func TestRequestIDAcceptsTheLongestLegalID(t *testing.T) {
	sent := strings.Repeat("a", MaxRequestIDLength)

	seen, _ := serveWithRequestID(t, sent)

	if seen != sent {
		t.Fatalf("handler saw %q, want the caller's %d-char id", seen, MaxRequestIDLength)
	}
}

// chi's minted id is "hostname/base62-000001". Echoing that to every caller
// would hand out the container's hostname and a per-process request counter,
// which is the third reason this middleware exists.
func TestMintedRequestIDLeaksNothingAboutTheHost(t *testing.T) {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		t.Skip("no hostname to check against")
	}

	seen, _ := serveWithRequestID(t, "")

	if strings.Contains(seen, hostname) {
		t.Fatalf("minted id %q contains the hostname %q", seen, hostname)
	}
	if acceptRequestID(seen) != seen {
		t.Fatalf("minted id %q would be refused on the way back in", seen)
	}
}

// Two requests must not share an id, or every correlation built on it is wrong.
func TestMintedRequestIDsDiffer(t *testing.T) {
	first, _ := serveWithRequestID(t, "")
	second, _ := serveWithRequestID(t, "")

	if first == second {
		t.Fatalf("two requests both got %q", first)
	}
}

// A response header set by the server is invisible to a script unless it is
// exposed, so the echo would arrive and be unreadable.
func TestCORSExposesTheRequestID(t *testing.T) {
	handler := CORS(CORSConfig{AllowedOrigins: []string{"https://shop.example"}})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	request := httptest.NewRequest(http.MethodGet, "/api/things", nil)
	request.Header.Set("Origin", "https://shop.example")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if got := recorder.Header().Get("Access-Control-Expose-Headers"); got != RequestIDHeader {
		t.Fatalf("Access-Control-Expose-Headers = %q, want %q", got, RequestIDHeader)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, RequestIDHeader) {
		t.Fatalf("Access-Control-Allow-Headers = %q, want it to include %q", got, RequestIDHeader)
	}
}

// nil means unset, an empty slice means none — the distinction TrustedProxies
// and APIPrefix already make in this package.
func TestCORSExposesNothingWhenToldNone(t *testing.T) {
	handler := CORS(CORSConfig{
		AllowedOrigins: []string{"https://shop.example"},
		ExposedHeaders: []string{},
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	request := httptest.NewRequest(http.MethodGet, "/api/things", nil)
	request.Header.Set("Origin", "https://shop.example")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if got := recorder.Header().Get("Access-Control-Expose-Headers"); got != "" {
		t.Fatalf("Access-Control-Expose-Headers = %q, want it unset", got)
	}
}
