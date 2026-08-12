package gateway

import (
	"testing"

	"github.com/chenyme/grok2api/backend/internal/domain/account"
	"github.com/chenyme/grok2api/backend/internal/domain/audit"
)

func TestPlanImageOutput(t *testing.T) {
	tests := []struct {
		name       string
		provider   account.Provider
		operation  audit.Operation
		requested  string
		resolution string
		target     int
	}{
		{name: "console generation 4k", provider: account.ProviderConsole, operation: audit.OperationImage, requested: "4k", resolution: "2k", target: 4096},
		{name: "console edit 4k", provider: account.ProviderConsole, operation: audit.OperationImageEdit, requested: "4K", resolution: "2k", target: 4096},
		{name: "web generation 4k", provider: account.ProviderWeb, operation: audit.OperationImage, requested: "4k", resolution: "2k", target: 4096},
		{name: "web edit 4k", provider: account.ProviderWeb, operation: audit.OperationImageEdit, requested: "4k", resolution: "1k", target: 4096},
		{name: "2k unchanged", provider: account.ProviderConsole, operation: audit.OperationImage, requested: "2k", resolution: "2k"},
		{name: "empty unchanged", provider: account.ProviderWeb, operation: audit.OperationImage, requested: "", resolution: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := planImageOutput(test.provider, test.operation, test.requested)
			if plan.upstreamResolution != test.resolution || plan.targetMaxEdge != test.target {
				t.Fatalf("plan = %#v, want resolution=%q target=%d", plan, test.resolution, test.target)
			}
		})
	}
}
