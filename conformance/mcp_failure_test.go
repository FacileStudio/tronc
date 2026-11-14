package conformance_test

import (
	stderrors "errors"
	"fmt"
	"testing"

	"github.com/FacileStudio/tronc/errors"
	"github.com/FacileStudio/tronc/mcp"
)

type mcpFailureInput struct {
	Code       string `json:"code,omitempty"`
	Message    string `json:"message,omitempty"`
	UpgradeURL string `json:"upgrade_url,omitempty"`
	Hint       string `json:"hint"`
	Raw        bool   `json:"raw,omitempty"`
	PlanLimit  string `json:"plan_limit_feature,omitempty"`
}

// planRefusal stands for porte's forfait.LimitError.
type planRefusal struct{ feature, link string }

func (p *planRefusal) Error() string                     { return "forfait: plan limit reached" }
func (p *planRefusal) PlanLimit() (feature, link string) { return p.feature, p.link }

func mcpFailureVectors(t *testing.T) []vector {
	t.Helper()
	const hint = "Call notes_list for the note ids."
	inputs := []struct {
		name  string
		input mcpFailureInput
	}{
		{"not_found_adds_hint", mcpFailureInput{Code: "not_found", Message: "note not found", Hint: hint}},
		{"not_found_without_hint", mcpFailureInput{Code: "not_found", Message: "note not found"}},
		{"invalid_argument_keeps_message", mcpFailureInput{Code: "invalid_argument", Message: "title is required", Hint: hint}},
		{"failed_precondition", mcpFailureInput{Code: "failed_precondition", Message: "only drafts can be sent", Hint: hint}},
		{"already_exists", mcpFailureInput{Code: "already_exists", Message: "a note with that name exists", Hint: hint}},
		{"permission_denied", mcpFailureInput{Code: "permission_denied", Message: "only the owner can delete it", Hint: hint}},
		{"rate_limited", mcpFailureInput{Code: "rate_limited", Message: "too many rooms this minute", Hint: hint}},
		{"resource_exhausted", mcpFailureInput{Code: "resource_exhausted", Message: "the file is too large", Hint: hint}},
		{"unavailable_keeps_message", mcpFailureInput{Code: "unavailable", Message: "bulle did not answer", Hint: hint}},
		{"internal_hidden", mcpFailureInput{Code: "internal", Message: "pq: connection refused", Hint: hint}},
		{"unauthenticated_hidden", mcpFailureInput{Code: "unauthenticated", Message: "missing auth", Hint: hint}},
		{"raw_error_hidden", mcpFailureInput{Raw: true, Message: "dial tcp: refused", Hint: hint}},
		{"accented_first_letter", mcpFailureInput{Code: "invalid_argument", Message: "échec de l'envoi", Hint: hint}},
		{"accented_not_found", mcpFailureInput{Code: "not_found", Message: "élément introuvable", Hint: hint}},
		{"payment_required_with_link", mcpFailureInput{Code: "payment_required", Message: "you used today's searches", UpgradeURL: "https://sso.example/spaces"}},
		{"payment_required_trailing_period", mcpFailureInput{Code: "payment_required", Message: "no AI on this plan.", UpgradeURL: "/billing"}},
		{"payment_required_without_link", mcpFailureInput{Code: "payment_required", Message: "no AI on this plan"}},
		{"payment_required_unsafe_link_dropped", mcpFailureInput{Code: "payment_required", Message: "no AI on this plan", UpgradeURL: "javascript:alert(1)"}},
		{"plan_limit_with_link", mcpFailureInput{PlanLimit: "rooms", UpgradeURL: "https://sso.example/spaces/7"}},
		{"plan_limit_without_link", mcpFailureInput{PlanLimit: "storage"}},
		{"plan_limit_known_key_readable", mcpFailureInput{PlanLimit: "nuage.storage_gb", UpgradeURL: "https://sso.example/spaces/7"}},
		{"plan_limit_unknown_key_raw", mcpFailureInput{PlanLimit: "plume.notes"}},
		{"plan_limit_unsafe_link_dropped", mcpFailureInput{PlanLimit: "rooms", UpgradeURL: "http://sso.example/spaces"}},
	}
	cases := make([]vector, 0, len(inputs))
	for _, entry := range inputs {
		got := mcp.Failure(failureError(entry.input), entry.input.Hint)
		cases = append(cases, vector{entry.name, entry.input, map[string]string{"text": got.Error()}})
	}
	return cases
}

func failureError(in mcpFailureInput) error {
	switch {
	case in.PlanLimit != "":
		return fmt.Errorf("creating: %w", &planRefusal{in.PlanLimit, in.UpgradeURL})
	case in.Raw:
		return stderrors.New(in.Message)
	}
	known := errors.New(in.Code, in.Message, nil)
	known.UpgradeURL = in.UpgradeURL
	return known
}
