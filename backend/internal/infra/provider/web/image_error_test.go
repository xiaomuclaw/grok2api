package web

import (
	"strings"
	"testing"

	"github.com/chenyme/grok2api/backend/internal/infra/provider"
)

func TestImagineWebSocketErrorClassifiesQuota(t *testing.T) {
	tests := []struct {
		name     string
		message  map[string]any
		quota    bool
		contains string
	}{
		{
			name:     "structured quota error",
			message:  map[string]any{"type": "error", "error": map[string]any{"code": "quota_exhausted", "message": "Image quota exhausted"}},
			quota:    true,
			contains: "quota_exhausted",
		},
		{
			name:     "string rate limit error",
			message:  map[string]any{"type": "error", "error": "rate limit reached, try later"},
			quota:    true,
			contains: "rate limit",
		},
		{
			name:     "top level code and message",
			message:  map[string]any{"type": "error", "code": "too_many_requests", "message": "too many requests"},
			quota:    true,
			contains: "too_many_requests",
		},
		{
			name:     "generic upstream error keeps detail",
			message:  map[string]any{"type": "error", "message": "internal websocket failure"},
			quota:    false,
			contains: "internal websocket failure",
		},
		{
			name:     "empty error is generic",
			message:  map[string]any{"type": "error"},
			quota:    false,
			contains: "Imagine WebSocket",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := imagineWebSocketError(test.message)
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := provider.IsQuotaError(err); got != test.quota {
				t.Fatalf("IsQuotaError(%v) = %t, want %t", err, got, test.quota)
			}
			if !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("error %q does not contain %q", err.Error(), test.contains)
			}
		})
	}
}
