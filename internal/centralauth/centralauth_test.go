package centralauth

import (
	"net/http"
	"testing"
)

func TestToken_EnvPrecedence(t *testing.T) {
	t.Setenv("CRNET_INGEST_TOKEN", "a")
	t.Setenv("INGEST_TOKEN", "b")
	t.Setenv("CENTRAL_TOKEN", "c")
	if got := Token(); got != "a" {
		t.Fatalf("got %q", got)
	}
	t.Setenv("CRNET_INGEST_TOKEN", "")
	if got := Token(); got != "b" {
		t.Fatalf("got %q", got)
	}
	t.Setenv("INGEST_TOKEN", "")
	if got := Token(); got != "c" {
		t.Fatalf("got %q", got)
	}
}

func TestApply_SetsHeadersWhenConfigured(t *testing.T) {
	t.Setenv("CRNET_INGEST_TOKEN", "secret")
	req, err := http.NewRequest(http.MethodPost, "http://example/v1/traces", nil)
	if err != nil {
		t.Fatal(err)
	}
	Apply(req)
	if got := req.Header.Get("Authorization"); got != "Bearer secret" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := req.Header.Get(Header); got != "secret" {
		t.Fatalf("header = %q", got)
	}
}

func TestApply_NoopWhenUnset(t *testing.T) {
	t.Setenv("CRNET_INGEST_TOKEN", "")
	t.Setenv("INGEST_TOKEN", "")
	t.Setenv("CENTRAL_TOKEN", "")
	req, _ := http.NewRequest(http.MethodPost, "http://example/v1/traces", nil)
	Apply(req)
	if req.Header.Get("Authorization") != "" {
		t.Fatal("expected no Authorization")
	}
	Apply(nil) // must not panic
}

func TestApply_BlankTokenIgnored(t *testing.T) {
	t.Setenv("CRNET_INGEST_TOKEN", "   ")
	t.Setenv("INGEST_TOKEN", "")
	t.Setenv("CENTRAL_TOKEN", "")
	req, _ := http.NewRequest(http.MethodGet, "http://example/", nil)
	Apply(req)
	if req.Header.Get(Header) != "" {
		t.Fatal("blank token should not set header")
	}
}
