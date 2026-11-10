package conformance_test

import (
	stderrors "errors"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/FacileStudio/tronc/errors"
	"github.com/FacileStudio/tronc/httpjson"
)

type errorInput struct {
	Constructor string `json:"constructor,omitempty"`
	Code        string `json:"code,omitempty"`
	Message     string `json:"message"`
	Cause       string `json:"cause,omitempty"`
	UpgradeURL  string `json:"upgrade_url,omitempty"`
	Wrapped     bool   `json:"wrapped,omitempty"`
	Raw         bool   `json:"raw,omitempty"`
}

type errorCase struct {
	name  string
	input errorInput
}

type errorWant struct {
	Status      int    `json:"status"`
	ContentType string `json:"content_type"`
	Body        string `json:"body"`
}

var constructors = []struct {
	name  string
	build func(string) *errors.Error
}{
	{"Invalid", errors.Invalid},
	{"Unauthorized", errors.Unauthorized},
	{"PaymentRequired", errors.PaymentRequired},
	{"Forbidden", errors.Forbidden},
	{"NotFound", errors.NotFound},
	{"Conflict", errors.Conflict},
	{"Failed", errors.Failed},
	{"TooLarge", errors.TooLarge},
	{"RateLimited", errors.RateLimited},
	{"NotAllowed", errors.NotAllowed},
	{"Unavailable", errors.Unavailable},
}

func errorVectors(t *testing.T) []vector {
	t.Helper()
	var inputs []errorCase
	for _, constructor := range constructors {
		code := constructor.build("").Code
		inputs = append(inputs, errorCase{code, errorInput{Constructor: constructor.name, Code: code, Message: "something went wrong"}})
	}
	inputs = append(inputs, []errorCase{
		{"internal_hides_cause", errorInput{Constructor: "Internal", Code: "internal", Message: "could not save", Cause: "pq: connection refused"}},
		{"unknown_code_is_500", errorInput{Code: "teapot", Message: "short and stout"}},
		{"empty_code_is_500", errorInput{Message: "no code"}},
		{"raw_error_is_internal", errorInput{Raw: true, Message: "pq: password authentication failed"}},
		{"wrapped_error_keeps_code", errorInput{Constructor: "NotFound", Code: "not_found", Message: "space not found", Wrapped: true}},
		{"message_html_escaped", errorInput{Constructor: "Invalid", Code: "invalid_argument", Message: "<b>name</b> & more"}},
		{"upgrade_url_https", errorInput{Constructor: "PaymentRequired", Code: "payment_required", Message: "plan required", UpgradeURL: "https://ardoise.facile.studio/billing"}},
		{"upgrade_url_path", errorInput{Constructor: "PaymentRequired", Code: "payment_required", Message: "plan required", UpgradeURL: "/billing?plan=pro"}},
		{"upgrade_url_javascript_dropped", errorInput{Constructor: "PaymentRequired", Code: "payment_required", Message: "plan required", UpgradeURL: "javascript:alert(1)"}},
		{"upgrade_url_protocol_relative_dropped", errorInput{Constructor: "PaymentRequired", Code: "payment_required", Message: "plan required", UpgradeURL: "//evil.example/billing"}},
		{"upgrade_url_http_dropped", errorInput{Constructor: "PaymentRequired", Code: "payment_required", Message: "plan required", UpgradeURL: "http://ardoise.facile.studio/billing"}},
		{"upgrade_url_credentials_dropped", errorInput{Constructor: "PaymentRequired", Code: "payment_required", Message: "plan required", UpgradeURL: "https://user:pass@ardoise.facile.studio/billing"}},
		{"upgrade_url_backslash_dropped", errorInput{Constructor: "PaymentRequired", Code: "payment_required", Message: "plan required", UpgradeURL: "/\\evil.example"}},
	}...)

	cases := make([]vector, 0, len(inputs))
	for _, entry := range inputs {
		recorder := httptest.NewRecorder()
		httpjson.WriteError(recorder, buildError(entry.input))
		cases = append(cases, vector{entry.name, entry.input, errorWant{
			Status:      recorder.Code,
			ContentType: recorder.Header().Get("Content-Type"),
			Body:        recorder.Body.String(),
		}})
	}
	return cases
}

func buildError(input errorInput) error {
	if input.Raw {
		return stderrors.New(input.Message)
	}
	var err *errors.Error
	switch input.Constructor {
	case "":
		err = errors.New(input.Code, input.Message, nil)
	case "Internal":
		err = errors.Internal(input.Message, stderrors.New(input.Cause))
	default:
		for _, constructor := range constructors {
			if constructor.name == input.Constructor {
				err = constructor.build(input.Message)
			}
		}
	}
	err.UpgradeURL = input.UpgradeURL
	if input.Wrapped {
		return fmt.Errorf("load space: %w", err)
	}
	return err
}
