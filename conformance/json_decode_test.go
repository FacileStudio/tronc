package conformance_test

import (
	"bytes"
	"compress/gzip"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FacileStudio/tronc/errors"
	"github.com/FacileStudio/tronc/httpjson"
)

type decodeTarget struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type decodeInput struct {
	Body        string `json:"body"`
	Gzip        bool   `json:"gzip"`
	Compressed  bool   `json:"compressed"`
	Limit       int64  `json:"limit"`
	ContentType string `json:"content_type"`
}

type decodeWant struct {
	Status  int           `json:"status"`
	Code    string        `json:"code,omitempty"`
	Message string        `json:"message,omitempty"`
	Decoded *decodeTarget `json:"decoded,omitempty"`
}

func jsonDecodeVectors(t *testing.T) []vector {
	t.Helper()
	const object = `{"name":"budget","count":3}`
	inputs := []struct {
		name  string
		input decodeInput
	}{
		{"valid", decodeInput{Body: object}},
		{"valid_trailing_whitespace", decodeInput{Body: object + " \n\t"}},
		{"valid_partial_object", decodeInput{Body: `{"name":"budget"}`}},
		{"unknown_field", decodeInput{Body: `{"name":"budget","owner":"x"}`}},
		{"field_name_matched_case_insensitively", decodeInput{Body: `{"NAME":"budget"}`}},
		{"trailing_object", decodeInput{Body: object + `{}`}},
		{"trailing_garbage", decodeInput{Body: object + ` x`}},
		{"empty_body", decodeInput{Body: ``}},
		{"not_json", decodeInput{Body: `name=budget`}},
		{"array_not_object", decodeInput{Body: `[1,2]`}},
		{"wrong_field_type", decodeInput{Body: `{"count":"three"}`}},
		{"null_body", decodeInput{Body: `null`}},
		{"at_limit", decodeInput{Body: object, Limit: int64(len(object))}},
		{"over_limit", decodeInput{Body: object, Limit: int64(len(object)) - 1}},
		{"wrong_content_type_still_decoded", decodeInput{Body: object, ContentType: "text/plain"}},
		{"gzip_valid", decodeInput{Body: object, Gzip: true, Compressed: true, Limit: 1 << 20}},
		{"gzip_at_decompressed_limit", decodeInput{Body: object, Gzip: true, Compressed: true, Limit: int64(len(object))}},
		{"gzip_over_decompressed_limit", decodeInput{Body: `{"name":"` + strings.Repeat("a", 4096) + `"}`, Gzip: true, Compressed: true, Limit: 1024}},
		{"gzip_unknown_field", decodeInput{Body: `{"owner":"x"}`, Gzip: true, Compressed: true, Limit: 1 << 20}},
		{"gzip_plain_body", decodeInput{Body: object, Gzip: true, Limit: 1 << 20}},
	}

	cases := make([]vector, 0, len(inputs))
	for _, entry := range inputs {
		if entry.input.ContentType == "" {
			entry.input.ContentType = "application/json"
		}
		cases = append(cases, vector{entry.name, entry.input, decode(t, entry.input)})
	}
	return cases
}

func decode(t *testing.T, input decodeInput) decodeWant {
	t.Helper()
	body := []byte(input.Body)
	if input.Compressed {
		body = compress(t, body)
	}
	request := httptest.NewRequest("POST", "/api/things", bytes.NewReader(body))
	request.Header.Set("Content-Type", input.ContentType)
	if input.Compressed {
		request.Header.Set("Content-Encoding", "gzip")
	}
	var target decodeTarget
	err := decodeWith(input, request, &target)
	if err == nil {
		return decodeWant{Status: 200, Decoded: &target}
	}
	var appErr *errors.Error
	if !stderrors.As(err, &appErr) {
		t.Fatalf("decode returned a non-envelope error: %v", err)
	}
	return decodeWant{Status: errors.Status(err), Code: appErr.Code, Message: appErr.Message}
}

func decodeWith(input decodeInput, request *http.Request, target *decodeTarget) error {
	recorder := httptest.NewRecorder()
	switch {
	case input.Gzip:
		return httpjson.DecodeGzipJSON(recorder, request, target, input.Limit)
	case input.Limit > 0:
		return httpjson.DecodeJSONLimit(recorder, request, target, input.Limit)
	default:
		return httpjson.DecodeJSON(recorder, request, target)
	}
}

func compress(t *testing.T, body []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
