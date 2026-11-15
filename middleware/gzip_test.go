package middleware

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func gzipGet(t *testing.T, accept string, handler http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	if accept != "" {
		request.Header.Set("Accept-Encoding", accept)
	}
	recorder := httptest.NewRecorder()
	Gzip(handler).ServeHTTP(recorder, request)
	return recorder
}

func TestGzipCompressesJSONAndDropsContentLength(t *testing.T) {
	recorder := gzipGet(t, "br, gzip;q=0.8", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "99")
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
	if recorder.Header().Get("Content-Encoding") != "gzip" || recorder.Header().Get("Content-Length") != "" {
		t.Fatalf("headers: %v", recorder.Header())
	}
	reader, err := gzip.NewReader(recorder.Body)
	if err != nil {
		t.Fatal(err)
	}
	if body, _ := io.ReadAll(reader); string(body) != `{"ok":true}` {
		t.Fatalf("body %q", body)
	}
}

func TestGzipLeavesOtherResponsesAlone(t *testing.T) {
	cases := map[string]struct {
		accept, contentType string
	}{
		"no accept":    {"", "application/json"},
		"q zero":       {"gzip;q=0", "application/json"},
		"image":        {"gzip", "image/png"},
		"event stream": {"gzip", "text/event-stream"},
	}
	for name, tc := range cases {
		recorder := gzipGet(t, tc.accept, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", tc.contentType)
			_, _ = io.WriteString(w, "plain")
		})
		if recorder.Header().Get("Content-Encoding") != "" || recorder.Body.String() != "plain" {
			t.Errorf("%s: response was compressed", name)
		}
	}
}

func TestGzipSkipsNoContentAndPreEncoded(t *testing.T) {
	recorder := gzipGet(t, "gzip", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	if recorder.Header().Get("Content-Encoding") != "" || recorder.Body.Len() != 0 {
		t.Fatal("204 must stay empty")
	}
	recorder = gzipGet(t, "gzip", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Encoding", "br")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "x")
	})
	if recorder.Header().Get("Content-Encoding") != "br" {
		t.Fatal("an encoded response must pass through")
	}
}

func TestGzipFlushAndSniff(t *testing.T) {
	recorder := gzipGet(t, "gzip", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("hello ", 10))
		w.(http.Flusher).Flush()
	})
	if recorder.Header().Get("Content-Encoding") != "gzip" || !recorder.Flushed {
		t.Fatal("a sniffed text body must compress and flush")
	}
}
