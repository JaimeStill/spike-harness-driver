package app

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/JaimeStill/spike-harness-driver/clutch/conform"
	"github.com/JaimeStill/spike-harness-driver/clutch/domain/session"
	"github.com/JaimeStill/spike-harness-driver/clutch/output"
	"github.com/JaimeStill/spike-harness-driver/clutch/scenario"
	"github.com/JaimeStill/spike-harness-driver/harness"
)

const versionTimeout = 30 * time.Second

type cellKey struct{ harness, provider string }

func matrix() []cellKey {
	var keys []cellKey
	for _, h := range harnesses {
		for _, p := range h.providers {
			keys = append(keys, cellKey{h.name, p})
		}
	}
	return keys
}

func selectCells(names, providers []string) ([]cellKey, error) {
	for _, n := range names {
		if _, err := lookupHarness(n); err != nil {
			return nil, fmt.Errorf("--harness: %w", err)
		}
	}
	all := matrix()
	known := map[string]bool{}
	for _, k := range all {
		known[k.provider] = true
	}
	for _, p := range providers {
		if !known[p] {
			var names []string
			for n := range known {
				names = append(names, n)
			}
			slices.Sort(names)
			return nil, fmt.Errorf("--provider: unknown provider %q (known: %s)", p, strings.Join(names, ", "))
		}
	}
	var keys []cellKey
	for _, k := range all {
		if (len(names) == 0 || slices.Contains(names, k.harness)) && (len(providers) == 0 || slices.Contains(providers, k.provider)) {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no cell runs --harness %s over --provider %s", strings.Join(names, ","), strings.Join(providers, ","))
	}
	return keys, nil
}

func newCell(base Config, k cellKey) (conform.Cell, error) {
	h, err := lookupHarness(k.harness)
	if err != nil {
		return conform.Cell{}, err
	}
	cfg := base
	cfg.Harness, cfg.Provider = k.harness, k.provider
	cfg.Model, cfg.HarnessVisionModel = "", ""
	cfg.Skills, cfg.Tools = nil, nil
	infra := newInfrastructure(&cfg)
	cell := conform.Cell{
		Harness:            k.harness,
		Provider:           k.provider,
		Profile:            h.profile,
		Pinned:             h.pinned,
		Version:            func(ctx context.Context) (string, error) { return harnessVersion(ctx, h.command) },
		Needs:              infra.Needs().Harness,
		Service:            session.New(infra.Driver, infra.Options),
		VisionModel:        infra.harnessVision(),
		DefaultTakesImages: infra.defaultTakesImages(),
		Models:             infra.Models,
	}
	if err := infra.Validate(); err != nil {
		cell.Needs = append([]scenario.Need{{What: "valid flags", Check: func(context.Context) error { return err }}}, cell.Needs...)
	}
	return cell, nil
}

func harnessVersion(ctx context.Context, command string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, command, "--version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s --version: %w", command, err)
	}
	return firstLine(string(out))
}

func firstLine(s string) (string, error) {
	for line := range strings.SplitSeq(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line, nil
		}
	}
	return "", errors.New("--version printed nothing")
}

func conformCommand(cfg *Config, out *output.Output) *cobra.Command {
	var (
		names, providers []string
		anyVersion       bool
		timeout          time.Duration
		events           bool
	)
	cmd := &cobra.Command{
		Use:   "conform",
		Short: "Run every harness over each of its providers and print a conformance matrix",
		Long: "conform runs the same capabilities over every harness and provider through the one\n" +
			"harness.Driver interface, and prints a matrix of what passed: a row per harness and\n" +
			"provider, a column per capability, each cell pass, FAIL, n/a, or skip, then the reason\n" +
			"for every cell that isn't a pass. The checks don't depend on the model's wording: they\n" +
			"ask for structured responses and compare values exactly, or read the exchange's events.\n\n" +
			"The cells are: " + cellList() + ".\n\n" +
			"A cell whose needs fail, such as LLAMA_BASE_URL unset, a missing az, or a harness not\n" +
			"on the PATH, is skipped with the reason, as is a harness whose version isn't the one\n" +
			"the suite pins (" + pinList() + "); --any-version runs it anyway. A skip doesn't\n" +
			"fail the run; a failed capability does, with a non-zero exit.\n\n" +
			"Each cell builds its own driver from the flags with that harness and provider, on the\n" +
			"harness's default models, so --model and --harness-vision-model, which name one\n" +
			"provider's model, and --skills and --tools, which would change what a session offers,\n" +
			"don't apply. --target still names the endpoint the direct client talks to, which the\n" +
			"audio-tool capability's tool transcribes with. This runs real models: the paid ones,\n" +
			"and the router's, which can take minutes to load one.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			keys, err := selectCells(names, providers)
			if err != nil {
				return err
			}
			cells := make([]conform.Cell, len(keys))
			for i, k := range keys {
				if cells[i], err = newCell(*cfg, k); err != nil {
					return err
				}
			}
			opts := conform.Options{
				AnyVersion: anyVersion,
				Timeout:    timeout,
				Progress: func(c conform.Cell, capability string) {
					if capability == "" {
						capability = "checking the harness"
					}
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "-- %s: %s\n", c.Label(), capability)
				},
			}
			if events {
				opts.Events = func(c conform.Cell, capability string, ev harness.Event) {
					if ev.Kind == harness.EventHarness && !cfg.All {
						return
					}
					out.Printf("%s %s | %s", c.Label(), capability, output.EventLine(ev))
				}
			}
			reports := conform.Run(cmd.Context(), cells, opts)
			conform.Render(cmd.OutOrStdout(), reports)
			if n := conform.Failures(reports); n > 0 {
				return fmt.Errorf("%d capabilities failed", n)
			}
			return nil
		},
	}
	fs := cmd.Flags()
	fs.StringSliceVar(&names, "harness", nil, "run only these harnesses; repeatable or comma-separated: "+strings.Join(harnessNames(), " | "))
	fs.StringSliceVar(&providers, "provider", nil, "run only over these providers; repeatable or comma-separated: "+providerList())
	fs.BoolVar(&anyVersion, "any-version", false, "run a harness whose version isn't the one the suite pins")
	fs.DurationVar(&timeout, "timeout", 10*time.Minute, "how long each capability may take, which a cold router model needs minutes of")
	fs.BoolVar(&events, "events", false, "print every exchange's events, tagged with the cell and capability, before the matrix")
	return cmd
}

func cellList() string {
	var parts []string
	for _, k := range matrix() {
		parts = append(parts, k.harness+"/"+k.provider)
	}
	return strings.Join(parts, ", ")
}

func pinList() string {
	parts := make([]string, len(harnesses))
	for i, h := range harnesses {
		parts[i] = h.name + " " + h.pinned
	}
	return strings.Join(parts, ", ")
}

func providerList() string {
	var ps []string
	for _, k := range matrix() {
		if !slices.Contains(ps, k.provider) {
			ps = append(ps, k.provider)
		}
	}
	return strings.Join(ps, " | ")
}
