package concurrency

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func TestAcquireHTTPDisabledConcurrency(t *testing.T) {
	mgr := NewManager()
	mgr.UpdateConfig(&config.Config{})
	release, exceeded := mgr.AcquireHTTP("key-1", "/v1/chat/completions")
	if exceeded != nil {
		t.Fatalf("expected no limit exceeded, got %v", exceeded)
	}
	release()
	if got := mgr.Snapshot().GlobalInFlight; got != 0 {
		t.Fatalf("expected zero inflight snapshot, got %d", got)
	}
}

func TestAcquireHTTPAppliesGlobalEndpointAndClientLimits(t *testing.T) {
	mgr := NewManager()
	mgr.UpdateConfig(&config.Config{
		SDKConfig: config.SDKConfig{
			APIKeys:              []string{"key-1"},
			ClientAPIKeyPolicies: []config.ClientAPIKeyPolicy{{APIKey: "key-1", Concurrency: 3}},
		},
		Concurrency: config.ConcurrencyConfig{
			Enabled:   true,
			Defaults:  config.ConcurrencyDefaults{GlobalInFlight: 5, ClientAPIKey: 2},
			Endpoints: []config.EndpointConcurrencyLimit{{Path: "/v1/chat/completions", InFlight: 4}},
		},
	})
	release, exceeded := mgr.AcquireHTTP("key-1", "/v1/chat/completions")
	if exceeded != nil {
		t.Fatalf("expected acquire to succeed, got %v", exceeded)
	}
	snapshot := mgr.Snapshot()
	if snapshot.GlobalInFlight != 1 {
		t.Fatalf("expected global inflight 1, got %d", snapshot.GlobalInFlight)
	}
	if snapshot.Endpoints["/v1/chat/completions"] != 1 {
		t.Fatalf("expected endpoint inflight 1, got %d", snapshot.Endpoints["/v1/chat/completions"])
	}
	if snapshot.ClientAPIKeys["key-1"] != 1 {
		t.Fatalf("expected client inflight 1, got %d", snapshot.ClientAPIKeys["key-1"])
	}
	release()
	snapshot = mgr.Snapshot()
	if snapshot.GlobalInFlight != 0 || len(snapshot.Endpoints) != 0 || len(snapshot.ClientAPIKeys) != 0 {
		t.Fatalf("expected all counters released, got %#v", snapshot)
	}
}

func TestAcquireHTTPRejectsEndpointSaturation(t *testing.T) {
	mgr := NewManager()
	mgr.UpdateConfig(&config.Config{
		Concurrency: config.ConcurrencyConfig{
			Enabled:   true,
			Endpoints: []config.EndpointConcurrencyLimit{{Path: "/v1/responses", InFlight: 1}},
		},
	})
	release, exceeded := mgr.AcquireHTTP("", "/v1/responses")
	if exceeded != nil {
		t.Fatalf("expected first acquire to succeed, got %v", exceeded)
	}
	defer release()
	_, exceeded = mgr.AcquireHTTP("", "/v1/responses")
	if exceeded == nil {
		t.Fatalf("expected second acquire to fail")
	}
	if exceeded.Code != "endpoint_concurrency_limit_exceeded" {
		t.Fatalf("expected endpoint limit code, got %q", exceeded.Code)
	}
}

func TestAcquireHTTPRejectsClientSaturation(t *testing.T) {
	mgr := NewManager()
	mgr.UpdateConfig(&config.Config{
		SDKConfig:   config.SDKConfig{ClientAPIKeyPolicies: []config.ClientAPIKeyPolicy{{APIKey: "key-1", Concurrency: 1}}},
		Concurrency: config.ConcurrencyConfig{Enabled: true},
	})
	release, exceeded := mgr.AcquireHTTP("key-1", "/v1/chat/completions")
	if exceeded != nil {
		t.Fatalf("expected first acquire to succeed, got %v", exceeded)
	}
	defer release()
	_, exceeded = mgr.AcquireHTTP("key-1", "/v1/chat/completions")
	if exceeded == nil {
		t.Fatalf("expected second acquire to fail")
	}
	if exceeded.Code != "client_concurrency_limit_exceeded" {
		t.Fatalf("expected client limit code, got %q", exceeded.Code)
	}
}

func TestAcquireUpstreamRelease(t *testing.T) {
	mgr := NewManager()
	release, exceeded := mgr.AcquireUpstream("auth-1", 1)
	if exceeded != nil {
		t.Fatalf("expected first upstream acquire to succeed, got %v", exceeded)
	}
	if got := mgr.Snapshot().UpstreamAccounts["auth-1"]; got != 1 {
		t.Fatalf("expected upstream inflight 1, got %d", got)
	}
	_, exceeded = mgr.AcquireUpstream("auth-1", 1)
	if exceeded == nil {
		t.Fatalf("expected second upstream acquire to fail")
	}
	release()
	if got := mgr.Snapshot().UpstreamAccounts["auth-1"]; got != 0 {
		t.Fatalf("expected upstream inflight 0 after release, got %d", got)
	}
}

func TestResolveUpstreamLimitMatchesConfigBackedAuthID(t *testing.T) {
	cfg := &config.Config{
		ClaudeKey: []config.ClaudeKey{{APIKey: "claude-key", BaseURL: "https://api.anthropic.com", Concurrency: 1}},
		GeminiKey: []config.GeminiKey{{APIKey: "gem-key", BaseURL: "https://generativelanguage.googleapis.com", Concurrency: 3}},
	}
	limits := buildUpstreamLimitMap(cfg)
	if len(limits) != 2 {
		t.Fatalf("expected two upstream limits, got %d", len(limits))
	}
	for authID, expected := range limits {
		got := ResolveUpstreamLimit(cfg, "", authID)
		if got != expected {
			t.Fatalf("expected auth %s to resolve limit %d, got %d", authID, expected, got)
		}
	}
}
