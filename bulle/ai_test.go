package bulle_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/FacileStudio/tronc/bulle"
)

func TestCompleteSendsTheTypedRequest(t *testing.T) {
	f := newFakeBulle(t, fixed(200, `{"json":{"label":"spam"},"model":"m1"}`))
	out, err := newClient(t, f).Complete(context.Background(), bulle.CompleteRequest{
		Prompt: "Classify", Input: "mail", Schema: []byte(`{"type":"object"}`), MaxTokens: 64, SpaceID: "s1",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := f.seen()
	want := `{"prompt":"Classify","input":"mail","schema":{"type":"object"},"max_tokens":64,"space_id":"s1"}`
	if got.Method != "POST" || got.RawPath != "/api/ai/complete" || got.Body != want || got.Auth != "Bearer tok" {
		t.Fatalf("request = %+v", got)
	}
	if string(out.JSON) != `{"label":"spam"}` || out.Model != "m1" {
		t.Fatalf("completion = %+v", out)
	}
}

func TestCompleteOmitsAnEmptySpace(t *testing.T) {
	f := newFakeBulle(t, fixed(200, `{"text":"hi","model":"m1"}`))
	if _, err := newClient(t, f).Complete(context.Background(), bulle.CompleteRequest{Prompt: "Hi"}); err != nil {
		t.Fatal(err)
	}
	if body := f.seen().Body; body != `{"prompt":"Hi"}` {
		t.Fatalf("body = %s", body)
	}
}

func TestDecideAIActsForTheUser(t *testing.T) {
	var actingFor string
	f := newFakeBulle(t, func(w http.ResponseWriter, r *http.Request) {
		actingFor = r.Header.Get(bulle.ActingForHeader)
		writeJSON(w, 200, `{"answers":{"project":{"choice":"a","confidence":0.9,"probabilities":{"a":0.9}}},"model":"jev","usage":{"input_tokens":3,"output_tokens":1}}`)
	})
	client := newClient(t, f)
	req := bulle.DecideRequest{
		ActingFor: " ada@example.com ", State: map[string]string{"title": "x"},
		Questions: map[string]bulle.Question{"project": {Type: "choice", Instructions: "Pick", Options: []string{"a", "b"}}},
	}
	decision, err := client.DecideAI(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	got := f.seen()
	want := `{"state":{"title":"x"},"questions":{"project":{"type":"choice","instructions":"Pick","options":["a","b"]}}}`
	if got.RawPath != "/api/ai/decide" || got.Body != want || actingFor != "ada@example.com" || got.Auth != "Bearer tok" {
		t.Fatalf("request = %+v acting for %q", got, actingFor)
	}
	if decision.Answers["project"].Choice != "a" || decision.Usage.InputTokens != 3 {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestDecideAIRunsAsTheUserWithoutActingFor(t *testing.T) {
	var actingFor string
	f := newFakeBulle(t, func(w http.ResponseWriter, r *http.Request) {
		actingFor = r.Header.Get(bulle.ActingForHeader)
		writeJSON(w, 200, `{"answers":{"project":{"choice":"b","confidence":0.8,"probabilities":{"b":0.8}}},"model":"jev","usage":{"input_tokens":3,"output_tokens":1}}`)
	})
	decision, err := newClient(t, f).DecideAI(context.Background(), bulle.DecideRequest{
		ActingFor: " ", State: map[string]string{"title": "x"},
		Questions: map[string]bulle.Question{"project": {Type: "choice", Instructions: "Pick", Options: []string{"a", "b"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := f.seen()
	if got.Method != "POST" || got.RawPath != "/api/ai/decide" || got.Auth != "Bearer tok" || actingFor != "" {
		t.Fatalf("request = %+v acting for %q", got, actingFor)
	}
	if decision.Answers["project"].Choice != "b" {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestDecideAIWithoutAIAccessIsPlanRequired(t *testing.T) {
	f := newFakeBulle(t, fixed(402, `{"error":{"code":"payment_required","message":"No AI","upgrade_url":"https://sso.example/spaces","key":"ai.access"}}`))
	_, err := newClient(t, f).DecideAI(context.Background(), bulle.DecideRequest{})
	var plan *bulle.PlanRequired
	if !errors.As(err, &plan) {
		t.Fatalf("err = %v, want *PlanRequired", err)
	}
}

func TestSummarizeRunsAsTheUser(t *testing.T) {
	var actingFor string
	f := newFakeBulle(t, func(w http.ResponseWriter, r *http.Request) {
		actingFor = r.Header.Get(bulle.ActingForHeader)
		writeJSON(w, 200, `{"summary":"It rains.","citations":[0],"model":"m1"}`)
	})
	out, err := newClient(t, f).Summarize(context.Background(), bulle.SummarizeRequest{
		Query: "weather", Sources: []bulle.SummarySource{{Title: "T", URL: "https://x.test", Snippet: "rain"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := f.seen()
	want := `{"query":"weather","sources":[{"title":"T","url":"https://x.test","snippet":"rain"}]}`
	if got.Method != "POST" || got.RawPath != "/api/ai/summarize" || got.Body != want || got.Auth != "Bearer tok" || actingFor != "" {
		t.Fatalf("request = %+v acting for %q", got, actingFor)
	}
	if out.Summary != "It rains." || len(out.Citations) != 1 || out.Citations[0] != 0 || out.Model != "m1" {
		t.Fatalf("summary = %+v", out)
	}
}

func TestRedirectsAreRefused(t *testing.T) {
	var leaked string
	target := newFakeBulle(t, func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("Authorization")
		writeJSON(w, 200, `{"text":"x","model":"m"}`)
	})
	f := newFakeBulle(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.server.URL+"/api/ai/complete", http.StatusTemporaryRedirect)
	})
	shared := &http.Client{}
	client, err := bulle.New(bulle.Config{URL: f.server.URL, TokenSource: staticToken, HTTPClient: shared})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Complete(context.Background(), bulle.CompleteRequest{Prompt: "x"}); !errors.Is(err, bulle.ErrUnreachable) {
		t.Fatalf("a redirect must fail as unreachable, err = %v", err)
	}
	if leaked != "" || target.seen().Requests != 0 {
		t.Fatal("the token must not follow a redirect")
	}
	if shared.CheckRedirect != nil {
		t.Fatal("the caller's client must not be changed")
	}
}

func TestDecideAIAsksTwoJudgesAndReadsEachAnswer(t *testing.T) {
	f := newFakeBulle(t, fixed(200, `{"answers":{"project":{"choice":"a","confidence":0.6,"probabilities":{},"agree":true,"judges":{`+
		`"jev":{"choice":"a","confidence":0.9,"probabilities":{"a":0.9,"b":0.1}},"clef":{"choice":"a","confidence":0.6}}},`+
		`"size":{"choice":"","confidence":0,"probabilities":{},"agree":false,"judges":{"jev":{"choice":"s","confidence":0.8},"clef":{"choice":"","confidence":0,"error":"did not answer"}}}},`+
		`"model":"jev-1","usage":{"input_tokens":8,"output_tokens":0,"cost":0.002},`+
		`"judges":{"jev":{"model":"jev-1","usage":{"input_tokens":4,"output_tokens":0,"cost":0.001}},"clef":{"error":"did not answer","usage":{"input_tokens":0,"output_tokens":0}}}}`))
	decision, err := newClient(t, f).DecideAI(context.Background(), bulle.DecideRequest{
		State:     map[string]string{"title": "x"},
		Questions: map[string]bulle.Question{"project": {Type: "choice", Instructions: "Pick", Options: []string{"a", "b"}}},
		Judges:    []string{bulle.JudgeJev, bulle.JudgeClef},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"state":{"title":"x"},"questions":{"project":{"type":"choice","instructions":"Pick","options":["a","b"]}},"judges":["jev","clef"]}`
	if body := f.seen().Body; body != want {
		t.Fatalf("body = %s", body)
	}
	project, size := decision.Answers["project"], decision.Answers["size"]
	if !project.Agreed() || project.Choice != "a" || project.Confidence != 0.6 || project.Judges[bulle.JudgeJev].Confidence != 0.9 || project.Judges[bulle.JudgeClef].Choice != "a" {
		t.Fatalf("project = %+v", project)
	}
	if size.Agreed() || size.Choice != "" || size.Judges[bulle.JudgeClef].Error != "did not answer" || size.Judges[bulle.JudgeJev].Choice != "s" {
		t.Fatalf("size = %+v", size)
	}
	if decision.Judges[bulle.JudgeClef].Error != "did not answer" || decision.Judges[bulle.JudgeJev].Model != "jev-1" || decision.Usage.Cost != 0.002 {
		t.Fatalf("judges = %+v usage = %+v", decision.Judges, decision.Usage)
	}
}

func TestDecideAISingleJudgeAnswerHasNoJudgeFields(t *testing.T) {
	f := newFakeBulle(t, fixed(200, `{"answers":{"project":{"choice":"a","confidence":0.9,"probabilities":{"a":0.9}}},"model":"jev","usage":{"input_tokens":3,"output_tokens":1}}`))
	decision, err := newClient(t, f).DecideAI(context.Background(), bulle.DecideRequest{
		State:     "x",
		Questions: map[string]bulle.Question{"project": {Type: "choice", Instructions: "Pick", Options: []string{"a", "b"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if body := f.seen().Body; strings.Contains(body, "judges") {
		t.Fatalf("a single-judge request must not carry judges: %s", body)
	}
	answer := decision.Answers["project"]
	if answer.Agreed() || answer.Agree != nil || answer.Judges != nil || decision.Judges != nil || answer.Choice != "a" {
		t.Fatalf("decision = %+v", decision)
	}
	encoded, _ := json.Marshal(decision)
	if strings.Contains(string(encoded), "judges") || strings.Contains(string(encoded), "agree") {
		t.Fatalf("a single-judge decision re-encodes with judge fields: %s", encoded)
	}
}

func TestAnOlderDecisionShapeStillReadsATwoJudgeAnswer(t *testing.T) {
	f := newFakeBulle(t, fixed(200, `{"answers":{"project":{"choice":"a","confidence":0.6,"probabilities":{},"agree":true,"judges":{"jev":{"choice":"a","confidence":0.9}}}},`+
		`"model":"jev-1","usage":{"input_tokens":8,"output_tokens":0},"judges":{"jev":{"model":"jev-1","usage":{"input_tokens":4,"output_tokens":0}}}}`))
	type legacyAnswer struct {
		Choice        string             `json:"choice"`
		Confidence    float64            `json:"confidence"`
		Probabilities map[string]float64 `json:"probabilities"`
	}
	var legacy struct {
		Answers map[string]legacyAnswer `json:"answers"`
		Model   string                  `json:"model"`
		Usage   bulle.Usage             `json:"usage"`
	}
	err := newClient(t, f).Call(context.Background(), bulle.Request{Method: http.MethodPost, Segments: []string{"ai", "decide"}, Body: map[string]any{}}, &legacy)
	if err != nil || legacy.Answers["project"].Choice != "a" || legacy.Model != "jev-1" || legacy.Usage.InputTokens != 8 {
		t.Fatalf("legacy = %+v err = %v", legacy, err)
	}
}
