package quota

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
)

func TestSQLiteStoreUsageSnapshotRoundTrip(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "quota.sqlite"), 0)
	if err != nil {
		t.Fatalf("OpenSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	timestamp := time.Date(2026, 4, 23, 12, 34, 56, 0, time.UTC)
	snapshot := usage.StatisticsSnapshot{
		TotalRequests: 1,
		SuccessCount:  1,
		FailureCount:  0,
		TotalTokens:   12,
		APIs: map[string]usage.APISnapshot{
			"k1": {
				TotalRequests: 1,
				TotalTokens:   12,
				Models: map[string]usage.ModelSnapshot{
					"gpt-5.4": {
						TotalRequests: 1,
						TotalTokens:   12,
						Details: []usage.RequestDetail{{
							Timestamp: timestamp,
							LatencyMs: 123,
							Source:    "openai",
							AuthIndex: "3",
							Tokens: usage.TokenStats{
								InputTokens:  5,
								OutputTokens: 7,
								TotalTokens:  12,
							},
						}},
					},
				},
			},
		},
		RequestsByDay:  map[string]int64{"2026-04-23": 1},
		RequestsByHour: map[string]int64{"12": 1},
		TokensByDay:    map[string]int64{"2026-04-23": 12},
		TokensByHour:   map[string]int64{"12": 12},
	}

	if err := store.StoreUsageSnapshot(snapshot); err != nil {
		t.Fatalf("StoreUsageSnapshot() error = %v", err)
	}
	loaded, ok, err := store.LoadUsageSnapshot()
	if err != nil {
		t.Fatalf("LoadUsageSnapshot() error = %v", err)
	}
	if !ok {
		t.Fatal("expected usage snapshot row to exist")
	}
	if loaded.TotalRequests != 1 || loaded.TotalTokens != 12 {
		t.Fatalf("unexpected loaded totals: %#v", loaded)
	}
	model := loaded.APIs["k1"].Models["gpt-5.4"]
	if model.TotalRequests != 1 || model.TotalTokens != 12 || len(model.Details) != 1 {
		t.Fatalf("unexpected loaded model snapshot: %#v", loaded.APIs)
	}
	if got := model.Details[0].Timestamp.UTC(); !got.Equal(timestamp) {
		t.Fatalf("expected timestamp %s, got %s", timestamp.Format(time.RFC3339Nano), got.Format(time.RFC3339Nano))
	}
}

func TestSQLiteStoreUsageSnapshotOverwrite(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "quota.sqlite"), 0)
	if err != nil {
		t.Fatalf("OpenSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.StoreUsageSnapshot(usage.StatisticsSnapshot{TotalRequests: 1, APIs: map[string]usage.APISnapshot{"k1": {}}}); err != nil {
		t.Fatalf("first StoreUsageSnapshot() error = %v", err)
	}
	if err := store.StoreUsageSnapshot(usage.StatisticsSnapshot{TotalRequests: 3, TotalTokens: 21, APIs: map[string]usage.APISnapshot{"k2": {TotalRequests: 3, TotalTokens: 21}}}); err != nil {
		t.Fatalf("second StoreUsageSnapshot() error = %v", err)
	}
	loaded, ok, err := store.LoadUsageSnapshot()
	if err != nil {
		t.Fatalf("LoadUsageSnapshot() error = %v", err)
	}
	if !ok {
		t.Fatal("expected usage snapshot row to exist")
	}
	if loaded.TotalRequests != 3 || loaded.TotalTokens != 21 {
		t.Fatalf("expected overwritten snapshot totals, got %#v", loaded)
	}
	if _, exists := loaded.APIs["k1"]; exists {
		t.Fatalf("expected previous snapshot contents to be replaced, got %#v", loaded.APIs)
	}
}
