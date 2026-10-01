package app

import (
	"github.com/spf13/cobra"

	"github.com/JaimeStill/spike-harness-driver/clutch/domain/session"
	"github.com/JaimeStill/spike-harness-driver/clutch/domain/workflow"
	"github.com/JaimeStill/spike-harness-driver/clutch/output"
)

// Domain holds the domain services.
type Domain struct {
	Session  *session.Service
	Workflow *workflow.Service
}

func newDomain(infra *Infrastructure) *Domain {
	return &Domain{
		Session:  session.New(infra.Driver, infra.Options),
		Workflow: workflow.New(infra.WorkflowRunner),
	}
}

// mountDomain builds the direct command families, one per domain.
func mountDomain(dom *Domain, out *output.Output) []*cobra.Command {
	return []*cobra.Command{session.Commands(dom.Session, out), workflow.Commands(dom.Workflow, out)}
}
