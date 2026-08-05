package errors

import (
	stderrors "errors"
	"fmt"
	"net/http"
	"testing"
)

func TestStatusCoversEveryConstructor(t *testing.T) {
	cases := map[string]struct {
		err  *Error
		want int
	}{
		"invalid_argument":    {Invalid("x"), http.StatusBadRequest},
		"unauthenticated":     {Unauthorized("x"), http.StatusUnauthorized},
		"permission_denied":   {Forbidden("x"), http.StatusForbidden},
		"not_found":           {NotFound("x"), http.StatusNotFound},
		"already_exists":      {Conflict("x"), http.StatusConflict},
		"failed_precondition": {Failed("x"), http.StatusPreconditionFailed},
		"resource_exhausted":  {TooLarge("x"), http.StatusRequestEntityTooLarge},
		"rate_limited":        {RateLimited("x"), http.StatusTooManyRequests},
		"internal":            {Internal("x", nil), http.StatusInternalServerError},
	}

	for code, testCase := range cases {
		if testCase.err.Code != code {
			t.Errorf("constructor for %s produced code %s", code, testCase.err.Code)
		}
		if got := Status(testCase.err); got != testCase.want {
			t.Errorf("Status(%s) = %d, want %d", code, got, testCase.want)
		}
	}

	if len(cases) != len(statusByCode) {
		t.Errorf("statusByCode has %d codes but %d are tested", len(statusByCode), len(cases))
	}
}

func TestStatusFallsBackToInternal(t *testing.T) {
	if got := Status(fmt.Errorf("plain")); got != http.StatusInternalServerError {
		t.Errorf("Status(plain error) = %d, want 500", got)
	}
	if got := Status(New("made_up_code", "x", nil)); got != http.StatusInternalServerError {
		t.Errorf("Status(unknown code) = %d, want 500", got)
	}
}

func TestUnwrapReachesTheCause(t *testing.T) {
	cause := fmt.Errorf("disk on fire")
	wrapped := fmt.Errorf("layer: %w", Internal("internal server error", cause))

	var appErr *Error
	if !stderrors.As(wrapped, &appErr) {
		t.Fatal("errors.As did not find the app error through a wrap")
	}
	if !stderrors.Is(appErr.Unwrap(), cause) {
		t.Error("Unwrap did not return the original cause")
	}
	if Status(wrapped) != http.StatusInternalServerError {
		t.Error("Status did not unwrap")
	}
}
