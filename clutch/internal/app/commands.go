package app

import (
	"github.com/spf13/cobra"

	"github.com/JaimeStill/spike-harness-driver/clutch/output"
	"github.com/JaimeStill/spike-harness-driver/clutch/scenario"
)

// commands is the list of mounts: the direct command families from domain.go, and the
// scenario mount and listing from scenarios.go. This file composes and does nothing else.
func commands(dom *Domain, scenarios []scenario.Scenario, out *output.Output) []*cobra.Command {
	var all []*cobra.Command
	all = append(all, mountDomain(dom, out)...)
	all = append(all, mountScenarios(scenarios, out), listCommand(scenarios))
	return all
}
