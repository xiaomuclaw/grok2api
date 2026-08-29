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
		{name: "console generation 2k", provider: account.ProviderConsole, operation: audit.OperationImage, requested: "2k", resolution: "2k", target: 2048},
		{name: "console edit 2k", provider: account.ProviderConsole, operation: audit.OperationImageEdit, requested: "2K", resolution: "2k", target: 2048},
		{name: "web generation 2k", provider: account.ProviderWeb, operation: audit.OperationImage, requested: "2k", resolution: "2k", target: 2048},
		{name: "web edit 2k", provider: account.ProviderWeb, operation: audit.OperationImageEdit, requested: "2k", resolution: "1k", target: 2048},
		{name: "1k unchanged", provider: account.ProviderConsole, operation: audit.OperationImage, requested: "1k", resolution: "1k"},
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
