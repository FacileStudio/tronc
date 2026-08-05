package httpjson

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FacileStudio/tronc/errors"
)

type payload struct {
	Name string `json:"name"`
}

func decode(t *testing.T, body string) error {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	var into payload
	return DecodeJSON(recorder, request, &into)
}

func TestDecodeJSONRejects(t *testing.T) {
	cases := map[string]struct {
		body string
		code string
	}{
		"unknown field":   {`{"name":"a","nope":1}`, "invalid_argument"},
		"malformed":       {`{`, "invalid_argument"},
		"trailing object": {`{"name":"a"}{"name":"b"}`, "invalid_argument"},
		"empty":           {``, "invalid_argument"},
		"oversized":       {fmt.Sprintf(`{"name":%q}`, strings.Repeat("x", int(MaxBodyBytes)+1)), "resource_exhausted"},
	}

	for name, testCase := range cases {
		err := decode(t, testCase.body)
		var appErr *errors.Error
		if err == nil {
			t.Errorf("%s: expected an error", name)
			continue
		}
		if !stderrors.As(err, &appErr) {
			t.Errorf("%s: error is not an *errors.Error: %v", name, err)
			continue
		}
		if appErr.Code != testCase.code {
			t.Errorf("%s: code = %s, want %s", name, appErr.Code, testCase.code)
		}
	}
}

func TestDecodeJSONAcceptsOneObject(t *testing.T) {
	if err := decode(t, `{"name":"tronc"}`); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

func TestWriteErrorAlwaysEmitsTheEnvelope(t *testing.T) {
	cases := map[string]struct {
		err     error
		status  int
		code    string
		message string
	}{
		"app error":   {errors.NotFound("space not found"), http.StatusNotFound, "not_found", "space not found"},
		"plain error": {fmt.Errorf("connection reset by peer"), http.StatusInternalServerError, "internal", "internal server error"},
	}

	for name, testCase := range cases {
		recorder := httptest.NewRecorder()
		WriteError(recorder, testCase.err)

		if recorder.Code != testCase.status {
			t.Errorf("%s: status = %d, want %d", name, recorder.Code, testCase.status)
		}
		if got := recorder.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("%s: content-type = %q", name, got)
		}

		var body struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: body is not JSON: %v", name, err)
		}
		if body.Error.Code != testCase.code || body.Error.Message != testCase.message {
			t.Errorf("%s: got {%s, %s}, want {%s, %s}",
				name, body.Error.Code, body.Error.Message, testCase.code, testCase.message)
		}
	}
}

func TestWriteErrorHidesTheUnderlyingCause(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteError(recorder, fmt.Errorf("pq: password authentication failed for user %q", "sablier"))

	if strings.Contains(recorder.Body.String(), "password") {
		t.Errorf("raw error text leaked to the client: %s", recorder.Body.String())
	}
}

func gzipped(t *testing.T, body string) *bytes.Buffer {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write([]byte(body)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return &buffer
}

func decodeGzip(t *testing.T, body *bytes.Buffer, maxDecompressed int64) error {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/", body)
	var into payload
	return DecodeGzipJSON(recorder, request, &into, maxDecompressed)
}

func TestDecodeGzipJSONAcceptsAValidBody(t *testing.T) {
	if err := decodeGzip(t, gzipped(t, `{"name":"tronc"}`), 1<<20); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

func TestDecodeGzipJSONRejectsPlainBody(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"tronc"}`))
	var into payload
	err := DecodeGzipJSON(recorder, request, &into, 1<<20)

	var appErr *errors.Error
	if err == nil || !stderrors.As(err, &appErr) || appErr.Code != "invalid_argument" {
		t.Fatalf("uncompressed body was accepted or misclassified: %v", err)
	}
}

func TestDecodeGzipJSONStopsADecompressionBomb(t *testing.T) {
	bomb := gzipped(t, fmt.Sprintf(`{"name":%q}`, strings.Repeat("x", 4<<20)))
	if bomb.Len() >= 1<<20 {
		t.Fatalf("test bomb is not actually small: %d compressed bytes", bomb.Len())
	}

	err := decodeGzip(t, bomb, 1<<10)
	var appErr *errors.Error
	if err == nil || !stderrors.As(err, &appErr) || appErr.Code != "resource_exhausted" {
		t.Fatalf("a %d-byte body expanding past the cap was accepted: %v", bomb.Len(), err)
	}
}

func TestDecodeJSONLimitHonoursAnExplicitCap(t *testing.T) {
	body := fmt.Sprintf(`{"name":%q}`, strings.Repeat("x", 2<<20))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	var into payload
	if err := DecodeJSONLimit(recorder, request, &into, 8<<20); err != nil {
		t.Fatalf("a 2MB body was refused under an 8MB cap: %v", err)
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	err := DecodeJSONLimit(recorder, request, &into, 1<<20)
	var appErr *errors.Error
	if err == nil || !stderrors.As(err, &appErr) || appErr.Code != "resource_exhausted" {
		t.Fatalf("a 2MB body was accepted under a 1MB cap: %v", err)
	}
}
