package bulle

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// ActingForHeader names, on a DecideAI call, the Bulle user the service
// acts for.
//
// Deprecated: call DecideAI with the user's exchanged token and no ActingFor.
const ActingForHeader = "X-Bulle-Acting-For"

// CompleteRequest is one completion for the signed-in user. Input is
// untrusted data the prompt works on, which Bulle fences off from the
// instructions; Schema, a JSON Schema, makes the answer a validated JSON
// value instead of text. SpaceID is the Registre space the request is for,
// whose plan may grant AI when the user's own does not; empty means the
// personal scope. Bulle caps System at 4 KiB, Prompt at 8 KiB, Input at
// 64 KiB and MaxTokens at 4096 (default 1024).
type CompleteRequest struct {
	System    string          `json:"system,omitempty"`
	Prompt    string          `json:"prompt"`
	Input     string          `json:"input,omitempty"`
	Schema    json.RawMessage `json:"schema,omitempty"`
	MaxTokens int             `json:"max_tokens,omitempty"`
	SpaceID   string          `json:"space_id,omitempty"`
}

// Completion is Bulle's answer: Text, or JSON when the request had a
// schema, and the model that wrote it.
type Completion struct {
	Text  string          `json:"text,omitempty"`
	JSON  json.RawMessage `json:"json,omitempty"`
	Model string          `json:"model"`
}

// Complete asks Bulle's POST /api/ai/complete for one completion, as the
// user the TokenSource speaks for. A user without AI access gets
// *PlanRequired; past the daily cap a 429 *StatusError.
func (c *Client) Complete(ctx context.Context, req CompleteRequest) (Completion, error) {
	var out Completion
	err := c.Call(ctx, Request{Method: http.MethodPost, Segments: []string{"ai", "complete"}, Body: req}, &out)
	return out, err
}

// Question is one typed question of a Decide call, in Bulle's decision
// contract: Type "choice" picks one of Options following Instructions.
type Question struct {
	Type         string   `json:"type"`
	Instructions any      `json:"instructions"`
	Criteria     any      `json:"criteria,omitempty"`
	Options      []string `json:"options,omitempty"`
}

// The judges a Decide call may ask. A call asks Jev alone unless Judges
// names both.
const (
	JudgeJev  = "jev"
	JudgeClef = "clef"
)

// DecideRequest asks Questions about State for the user the client's
// TokenSource speaks for. Judges is optional: empty or {JudgeJev} asks Jev
// alone, and {JudgeJev, JudgeClef} asks both at once. The response then holds
// each judge's answer and whether they agree. A two-judge call counts as two
// against Bulle's rate limit and the user's AI budget.
type DecideRequest struct {
	// Deprecated: leave ActingFor empty and call with the user's exchanged
	// token. Set, it names the user a service-key call acts for.
	ActingFor string              `json:"-"`
	State     any                 `json:"state"`
	Questions map[string]Question `json:"questions"`
	Judges    []string            `json:"judges,omitempty"`
}

// Answer is one choice question's result. With two judges, Judges holds each
// judge's own answer and Agree says whether they picked the same option;
// Choice is then set only when they agree, Confidence is the lower of the two
// (0 when a judge gave no answer) and Probabilities is empty. A single-judge
// answer carries neither Agree nor Judges.
type Answer struct {
	Choice        string                 `json:"choice"`
	Confidence    float64                `json:"confidence"`
	Probabilities map[string]float64     `json:"probabilities"`
	Agree         *bool                  `json:"agree,omitempty"`
	Judges        map[string]JudgeAnswer `json:"judges,omitempty"`
}

// Agreed reports whether a two-judge answer found both judges on the same
// option. It is false for a single-judge answer and when a judge failed.
func (a Answer) Agreed() bool { return a.Agree != nil && *a.Agree }

// JudgeAnswer is what one judge answered to one question, or why it did not:
// Error is "not configured" or "did not answer".
type JudgeAnswer struct {
	Choice        string             `json:"choice"`
	Confidence    float64            `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Error         string             `json:"error,omitempty"`
}

// JudgeCall is what one judge's call served and billed, or why it failed:
// Error is "not configured" for a judge the server holds no key for, and
// "did not answer" for one whose call failed.
type JudgeCall struct {
	Model string `json:"model,omitempty"`
	Usage Usage  `json:"usage"`
	Error string `json:"error,omitempty"`
}

// Usage is what one DecideAI call billed.
type Usage struct {
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	Cost         float64 `json:"cost,omitempty"`
}

// Decision is every answer, keyed by its question's name, the model that
// served them and the usage. With two judges, Model is Jev's, Usage is the sum
// of both calls and Judges holds each call on its own.
type Decision struct {
	Answers map[string]Answer    `json:"answers"`
	Model   string               `json:"model"`
	Usage   Usage                `json:"usage"`
	Judges  map[string]JudgeCall `json:"judges,omitempty"`
}

// DecideAI asks Bulle's POST /api/ai/decide as the user the TokenSource
// speaks for, with an exchanged token. A user without AI access gets
// *PlanRequired; a 429 is a *StatusError. A request that sets the deprecated
// ActingFor runs as the app's service instead, with the service key Bulle
// knows the app by as the TokenSource.
func (c *Client) DecideAI(ctx context.Context, req DecideRequest) (Decision, error) {
	call := Request{Method: http.MethodPost, Segments: []string{"ai", "decide"}, Body: req}
	if actingFor := strings.TrimSpace(req.ActingFor); actingFor != "" {
		call.Header = http.Header{ActingForHeader: {actingFor}}
	}
	var out Decision
	err := c.Call(ctx, call, &out)
	return out, err
}

// SummarySource is one document Summarize cites.
type SummarySource struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// SummarizeRequest asks for an answer to Query drawn from Sources. Bulle
// takes 1 to 10 sources and a query of up to 500 bytes.
type SummarizeRequest struct {
	Query   string          `json:"query"`
	Sources []SummarySource `json:"sources"`
}

// Summary is Bulle's answer, the indexes of the Sources it cites and the
// model that wrote it.
type Summary struct {
	Summary   string `json:"summary"`
	Citations []int  `json:"citations"`
	Model     string `json:"model"`
}

// Summarize asks Bulle's POST /api/ai/summarize, as the user the TokenSource
// speaks for. A user without AI access gets *PlanRequired; a 429 is a
// *StatusError.
func (c *Client) Summarize(ctx context.Context, req SummarizeRequest) (Summary, error) {
	var out Summary
	err := c.Call(ctx, Request{Method: http.MethodPost, Segments: []string{"ai", "summarize"}, Body: req}, &out)
	return out, err
}
