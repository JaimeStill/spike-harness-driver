package app

import (
	"github.com/spf13/cobra"

	"github.com/JaimeStill/spike-harness-driver/domain/session"
	"github.com/JaimeStill/spike-harness-driver/output"
)

// Domain holds the domain services.
type Domain struct {
	Session *session.Service
}

func newDomain(infra *Infrastructure) *Domain {
	return &Domain{Session: session.New(infra.Driver, infra.Options)}
}

// mountDomain builds the direct command families, one per domain.
func mountDomain(dom *Domain, out *output.Output) []*cobra.Command {
	return []*cobra.Command{session.Commands(dom.Session, out)}
}
