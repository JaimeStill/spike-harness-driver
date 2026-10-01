package app

import (
	"testing"

	"github.com/JaimeStill/spike-harness-driver/workflow"
)

func TestSessionConfig(t *testing.T) {
	base := Config{Harness: harnessPi, Provider: providerAzure, Model: "gpt", HarnessVisionModel: "v"}
	tests := []struct {
		name string
		spec workflow.SessionSpec
		want Config
	}{
		{"the flags'", workflow.SessionSpec{}, base},
		{"same harness", workflow.SessionSpec{Harness: harnessPi}, base},
		{"another harness", workflow.SessionSpec{Harness: harnessClaude}, Config{Harness: harnessClaude}},
		{"another harness and model", workflow.SessionSpec{Harness: harnessClaude, Model: "sonnet"}, Config{Harness: harnessClaude, Model: "sonnet"}},
		{"another provider", workflow.SessionSpec{Provider: providerLlama}, Config{Harness: harnessPi, Provider: providerLlama}},
		{"same provider, another model", workflow.SessionSpec{Provider: providerAzure, Model: "mini"}, Config{Harness: harnessPi, Provider: providerAzure, Model: "mini", HarnessVisionModel: "v"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sessionConfig(base, tt.spec)
			if got.Harness != tt.want.Harness || got.Provider != tt.want.Provider || got.Model != tt.want.Model || got.HarnessVisionModel != tt.want.HarnessVisionModel {
				t.Errorf("sessionConfig = %+v, want %+v", got, tt.want)
			}
		})
	}
}
