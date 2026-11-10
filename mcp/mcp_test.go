package mcp

import (
	"context"
	stderrors "errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/FacileStudio/tronc/errors"
	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// noop is a read-only handler.
func noop(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, struct{}, error) {
	return nil, struct{}{}, nil
}

// acceptAll is a verifier for option checks.
func acceptAll(context.Context, string, *http.Request) (*auth.TokenInfo, error) {
	return &auth.TokenInfo{UserID: "1"}, nil
}

// TestBuildRefusesBadTools checks names, duplicates, titles and annotations.
func TestBuildRefusesBadTools(t *testing.T) {
	read := &Annotations{ReadOnly: true}
	cases := map[string]ToolSpec{
		"no prefix":      {Name: "list", Title: "T", Description: "D", Annotations: read},
		"other prefix":   {Name: "other_list", Title: "T", Description: "D", Annotations: read},
		"upper case":     {Name: "app_List", Title: "T", Description: "D", Annotations: read},
		"no annotations": {Name: "app_list", Title: "T", Description: "D"},
		"no title":       {Name: "app_list", Description: "D", Annotations: read},
		"read-only and destructive": {Name: "app_list", Title: "T", Description: "D",
			Annotations: &Annotations{ReadOnly: true, Destructive: true}},
	}
	for name, spec := range cases {
		_, err := build(Options{Name: "app", Resource: "https://app.example/mcp", Issuer: "https://i.example", Verifier: acceptAll,
			Tools: []Tool{NewTool(spec, noop)}})
		if err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	twice := NewTool(ToolSpec{Name: "app_list", Title: "T", Description: "D", Annotations: read}, noop)
	if _, err := build(Options{Name: "app", Resource: "https://app.example/mcp", Issuer: "https://i.example", Verifier: acceptAll,
		Tools: []Tool{twice, twice}}); err == nil {
		t.Error("a duplicate tool must be refused")
	}
}

// TestBuildRefusesBadOptions checks the required options.
func TestBuildRefusesBadOptions(t *testing.T) {
	good := Options{Name: "app", Resource: "https://app.example/mcp", Issuer: "https://i.example", Verifier: acceptAll}
	cases := map[string]func(*Options){
		"no verifier":       func(o *Options) { o.Verifier = nil },
		"relative resource": func(o *Options) { o.Resource = "/mcp" },
		"no issuer":         func(o *Options) { o.Issuer = "" },
		"bad name":          func(o *Options) { o.Name = "My App" },
		"half scopes":       func(o *Options) { o.Scopes = Scopes{Read: "app.read"} },
	}
	for name, change := range cases {
		opts := good
		change(&opts)
		if _, err := build(opts); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	s, err := build(good)
	if err != nil || s.opts.Scopes.Write != "app.write" || s.metadataURL != "https://app.example/.well-known/oauth-protected-resource/mcp" {
		t.Fatalf("want default scopes and the RFC 9728 URL, got %v %+v %s", err, s.opts.Scopes, s.metadataURL)
	}
}

// TestFailureKeepsOnlyActionableText checks the error mapping.
func TestFailureKeepsOnlyActionableText(t *testing.T) {
	if got := Failure(errors.NotFound("event not found"), "Call app_list."); got.Error() != "Event not found. Call app_list." {
		t.Errorf("not_found: got %q", got)
	}
	if got := Failure(errors.Invalid("title is required"), "x"); got.Error() != "Title is required" {
		t.Errorf("invalid: got %q", got)
	}
	if got := Failure(errors.Internal("db down", context.Canceled), "x"); !stderrors.Is(got, ErrUnavailable) {
		t.Errorf("internal: got %q", got)
	}
	if Failure(nil, "x") != nil {
		t.Error("nil must stay nil")
	}
}

// TestSignerRefusesForgeries checks tampering, another person and expiry.
func TestSignerRefusesForgeries(t *testing.T) {
	s := signer{key: []byte("k"), ttl: time.Minute}
	now := time.Now()
	state := s.sign("7", "abc", now.Add(time.Minute))
	if id, err := s.verify(state, "7", now); err != nil || id != "abc" {
		t.Fatalf("own state: got %q %v", id, err)
	}
	forged := strings.Replace(state, ".abc.", ".abd.", 1)
	for name, check := range map[string]func() error{
		"tampered":   func() error { _, err := s.verify(forged, "7", now); return err },
		"other user": func() error { _, err := s.verify(state, "8", now); return err },
		"expired":    func() error { _, err := s.verify(state, "7", now.Add(2*time.Minute)); return err },
		"other key":  func() error { _, err := (signer{key: []byte("j")}).verify(state, "7", now); return err },
		"bare id":    func() error { _, err := s.verify("abc", "7", now); return err },
	} {
		if !stderrors.Is(check(), ErrConfirmation) {
			t.Errorf("%s: want ErrConfirmation", name)
		}
	}
}

// TestCallsAnyReadsBatches checks the write gate sees calls in a batch.
func TestCallsAnyReadsBatches(t *testing.T) {
	writes := map[string]bool{"app_write": true}
	batch := `[{"method":"tools/list"},{"method":"tools/call","params":{"name":"app_write"}}]`
	if !callsAny([]byte(batch), writes) || callsAny([]byte(`{"method":"tools/call","params":{"name":"app_read"}}`), writes) {
		t.Fatal("the gate must catch a write call in a batch and pass a read")
	}
}

// TestClipAndFit checks truncation on a rune boundary and the budget search.
func TestClipAndFit(t *testing.T) {
	if got, cut := Clip("héllo", 2); got != "h" || !cut {
		t.Errorf("clip must stop before a split rune, got %q %v", got, cut)
	}
	if n := FitCount(1000, func(n int) int { return n * 100 }); n != ResultBudget/100 {
		t.Errorf("fit: got %d", n)
	}
	var position struct{ Offset int }
	if err := DecodeCursor(EncodeCursor(struct{ Offset int }{40}), &position); err != nil || position.Offset != 40 {
		t.Errorf("cursor round trip: got %+v %v", position, err)
	}
	if !stderrors.Is(DecodeCursor("!!", &position), ErrCursor) {
		t.Error("a bad cursor must be ErrCursor")
	}
}
