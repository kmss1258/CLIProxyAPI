package openai

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
)

func TestCollectImagesFromResponsesStreamUsesOutputItemDoneWhenCompletedOutputEmpty(t *testing.T) {
	dataChan := make(chan []byte, 2)
	errChan := make(chan *interfaces.ErrorMessage, 1)

	dataChan <- []byte("data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"ig_123\",\"type\":\"image_generation_call\",\"status\":\"generating\",\"background\":\"opaque\",\"output_format\":\"png\",\"quality\":\"low\",\"size\":\"1024x1024\",\"result\":\"ZmFrZS1iNjQ=\",\"revised_prompt\":\"revised\"}}\n\n")
	dataChan <- []byte("data: {\"type\":\"response.completed\",\"response\":{\"created_at\":1777223489,\"output\":[],\"tool_usage\":{\"image_gen\":{\"total_tokens\":264}}}}\n\n")
	close(dataChan)
	close(errChan)

	out, errMsg := collectImagesFromResponsesStream(context.Background(), dataChan, errChan, "b64_json")
	if errMsg != nil {
		t.Fatalf("collectImagesFromResponsesStream() unexpected error: %+v", errMsg)
	}

	var payload struct {
		Created int64 `json:"created"`
		Data    []struct {
			B64JSON       string `json:"b64_json"`
			RevisedPrompt string `json:"revised_prompt"`
		} `json:"data"`
		Usage struct {
			TotalTokens int64 `json:"total_tokens"`
		} `json:"usage"`
		Size    string `json:"size"`
		Quality string `json:"quality"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, string(out))
	}
	if payload.Created != 1777223489 {
		t.Fatalf("created = %d, want 1777223489", payload.Created)
	}
	if len(payload.Data) != 1 {
		t.Fatalf("len(data) = %d, want 1", len(payload.Data))
	}
	if payload.Data[0].B64JSON != "ZmFrZS1iNjQ=" {
		t.Fatalf("b64_json = %q, want %q", payload.Data[0].B64JSON, "ZmFrZS1iNjQ=")
	}
	if payload.Data[0].RevisedPrompt != "revised" {
		t.Fatalf("revised_prompt = %q, want %q", payload.Data[0].RevisedPrompt, "revised")
	}
	if payload.Usage.TotalTokens != 264 {
		t.Fatalf("usage.total_tokens = %d, want 264", payload.Usage.TotalTokens)
	}
	if payload.Size != "1024x1024" || payload.Quality != "low" {
		t.Fatalf("unexpected metadata: size=%q quality=%q", payload.Size, payload.Quality)
	}
}

func TestExtractImageCallResultFromOutputItemDoneRejectsNonImageItems(t *testing.T) {
	if _, ok := extractImageCallResultFromOutputItemDone([]byte(`{"type":"response.output_item.done","item":{"type":"message","content":[]}}`)); ok {
		t.Fatal("expected non-image output item to be rejected")
	}
}

func TestExtractImagesCompletionMetadataRejectsWrongEventType(t *testing.T) {
	if _, _, err := extractImagesCompletionMetadata([]byte(`{"type":"response.in_progress"}`)); err == nil {
		t.Fatal("expected wrong event type to fail")
	}
}

func TestCollectImagesFromResponsesStreamContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dataChan := make(chan []byte)
	errChan := make(chan *interfaces.ErrorMessage)
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	_, errMsg := collectImagesFromResponsesStream(ctx, dataChan, errChan, "b64_json")
	if errMsg == nil || errMsg.StatusCode == 0 {
		t.Fatalf("expected timeout-style error on context cancel, got %+v", errMsg)
	}
}
