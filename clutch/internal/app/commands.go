package app

import (
	"github.com/spf13/cobra"

	"github.com/JaimeStill/spike-harness-driver/clutch/output"
	"github.com/JaimeStill/spike-harness-driver/clutch/scenario"
)

func commands(cfg *Config, dom *Domain, scenarios []scenario.Scenario, out *output.Output) []*cobra.Command {
	var all []*cobra.Command
	all = append(all, mountDomain(dom, out)...)
	all = append(all, mountScenarios(scenarios, out), listCommand(scenarios), conformCommand(cfg, out))
	return all
}
