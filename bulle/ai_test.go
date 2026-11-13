package bulle_test

import (
	"context"
	"errors"
	"net/http"
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

func TestDecideActsForTheUser(t *testing.T) {
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
	req.ActingFor = " "
	if _, err := client.DecideAI(context.Background(), req); !errors.Is(err, bulle.ErrNoActingFor) || f.seen().Requests != 1 {
		t.Fatalf("a missing user must send nothing, err = %v", err)
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
