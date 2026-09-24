package scenario

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/pflag"
)

// Scenario is one narrated capability: what it shows, what must be in place, and the steps
// that show it.
type Scenario struct {
	Name    string // the word after "clutch scenario"
	Summary string // the line the listing prints
	Needs   []Need
	// Flags binds the scenario's own flags; nil for none.
	Flags func(*pflag.FlagSet)
	// Steps builds the steps of one run, and the cleanup that runs after them however they
	// end. The cleanup may be nil.
	Steps func() (steps []Step, cleanup func() error)
}

// Need is a precondition a scenario checks before its first step.
type Need struct {
	What  string
	Check func(context.Context) error
}

// Step is one beat of the narration: what is about to happen, and the action that does it.
type Step struct {
	Intent string
	Action func(context.Context, *Reporter) error
}

// Run checks every need of s, then runs its steps in order, narrating each intent through r
// before its action. It stops at the first need that fails or the first action that returns
// an error, and runs the cleanup either way once the steps have been built.
func Run(ctx context.Context, s Scenario, r *Reporter) (err error) {
	for _, n := range s.Needs {
		if cerr := n.Check(ctx); cerr != nil {
			return fmt.Errorf("%s: need %s: %w", s.Name, n.What, cerr)
		}
	}
	steps, cleanup := s.Steps()
	if cleanup != nil {
		defer func() {
			if cerr := cleanup(); cerr != nil {
				err = errors.Join(err, fmt.Errorf("%s: cleanup: %w", s.Name, cerr))
			}
		}()
	}
	for i, step := range steps {
		if err := ctx.Err(); err != nil {
			return err
		}
		r.Intent(i+1, len(steps), step.Intent)
		if err := step.Action(ctx, r); err != nil {
			return fmt.Errorf("%s: step %d: %w", s.Name, i+1, err)
		}
	}
	return nil
}
