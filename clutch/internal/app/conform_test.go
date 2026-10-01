package app

import (
	"slices"
	"strings"
	"testing"
)

func TestSelectCells(t *testing.T) {
	all := []cellKey{
		{"pi", "llama.cpp"}, {"pi", "azure"},
		{"claude", "anthropic"},
		{"opencode", "llama.cpp"}, {"opencode", "azure"},
	}
	tests := []struct {
		name             string
		harness, provide []string
		want             []cellKey
		wantErr          string
	}{
		{"no filters", nil, nil, all, ""},
		{"a harness", []string{"opencode"}, nil, []cellKey{{"opencode", "llama.cpp"}, {"opencode", "azure"}}, ""},
		{"two harnesses", []string{"claude", "pi"}, nil, []cellKey{{"pi", "llama.cpp"}, {"pi", "azure"}, {"claude", "anthropic"}}, ""},
		{"a provider", nil, []string{"azure"}, []cellKey{{"pi", "azure"}, {"opencode", "azure"}}, ""},
		{"both", []string{"pi"}, []string{"azure"}, []cellKey{{"pi", "azure"}}, ""},
		{"a provider no harness runs over", nil, []string{"anthropic", "azure"}, []cellKey{{"pi", "azure"}, {"claude", "anthropic"}, {"opencode", "azure"}}, ""},
		{"an unknown harness", []string{"nope"}, nil, nil, `--harness: unknown harness "nope"`},
		{"an unknown provider", nil, []string{"openai"}, nil, `--provider: unknown provider "openai" (known: anthropic, azure, llama.cpp)`},
		{"filters with no cell", []string{"claude"}, []string{"azure"}, nil, "no cell runs --harness claude over --provider azure"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := selectCells(tt.harness, tt.provide)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || !slices.Equal(got, tt.want) {
				t.Errorf("got %v, err %v, want %v", got, err, tt.want)
			}
		})
	}
}

func TestEachCellBuildsItsOwnInfrastructure(t *testing.T) {
	base := Config{
		Harness: "claude", Provider: "anthropic", Model: "m", HarnessVisionModel: "v",
		Target: targetLlama, State: t.TempDir(), Skills: []string{"x"}, Tools: []string{"y"},
	}
	llama, err := newCell(base, cellKey{"opencode", providerLlama})
	if err != nil {
		t.Fatal(err)
	}
	azure, err := newCell(base, cellKey{"opencode", providerAzure})
	if err != nil {
		t.Fatal(err)
	}
	if base.Harness != "claude" || base.Provider != "anthropic" || base.Model != "m" || len(base.Skills) != 1 {
		t.Errorf("the base config changed: %+v", base)
	}
	if llama.Pinned != "1.18.34" || llama.Profile.Name != "OpenCode" {
		t.Errorf("cell = %+v", llama)
	}
	if llama.VisionModel != qwenVision || llama.DefaultTakesImages {
		t.Errorf("llama.cpp: vision %q, default takes images %v", llama.VisionModel, llama.DefaultTakesImages)
	}
	if azure.VisionModel != azureModel || !azure.DefaultTakesImages {
		t.Errorf("azure: vision %q, default takes images %v", azure.VisionModel, azure.DefaultTakesImages)
	}
	// The base's --skills and --tools are left out of what every session offers.
	if got := llama.Service.Skills(); len(got) != 0 {
		t.Errorf("skills = %v", got)
	}
	if got := llama.Service.Tools(); len(got) != 0 {
		t.Errorf("tools = %v", got)
	}
}

func TestACellWhoseFlagsDontValidateIsSkippedNotFailed(t *testing.T) {
	cell, err := newCell(Config{Target: targetLlama, State: t.TempDir(), AzureScope: "not a scope"}, cellKey{"pi", providerAzure})
	if err != nil {
		t.Fatal(err)
	}
	if err := cell.Needs[0].Check(t.Context()); err == nil || !strings.Contains(err.Error(), "--azure-scope") {
		t.Errorf("first need: %v", err)
	}
	ok, err := newCell(Config{Target: targetLlama, State: t.TempDir(), AzureScope: "not a scope"}, cellKey{"pi", providerLlama})
	if err != nil {
		t.Fatal(err)
	}
	if err := ok.Needs[0].Check(t.Context()); err != nil && strings.Contains(err.Error(), "--azure-scope") {
		t.Errorf("the llama.cpp cell took the azure scope: %v", err)
	}
}

func TestFirstLine(t *testing.T) {
	for in, want := range map[string]string{
		"0.99.2\n":                       "0.99.2",
		"\n  2.1.286 (Claude Code)\nx\n": "2.1.286 (Claude Code)",
	} {
		if got, err := firstLine(in); err != nil || got != want {
			t.Errorf("firstLine(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := firstLine(" \n"); err == nil {
		t.Error("blank output has a first line")
	}
}
