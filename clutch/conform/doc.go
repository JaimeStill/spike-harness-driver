// Package conform is clutch's conformance suite: it runs every harness over each of its
// providers through the one harness.Driver interface, tries the same capabilities on each, and
// reports a matrix of what passed.
//
// The scenario package narrates what a capability looks like; this package judges whether it
// works. A check here never depends on a model's wording, which varies from model to model and
// run to run. A capability asks for a structured response and compares its values exactly, to
// case and spacing, or reads the events: how the exchange stopped, which tools were called.
// The few judgments left are about shapes rather than words, such as accepting "rectangle" for
// the fixture's square.
//
// A Cell is one harness over one provider, with everything the suite needs to drive it. The
// composition root builds the cells, because it alone knows the adapters; Run takes them from
// there. A cell whose preconditions fail, or whose harness isn't the pinned version, is
// skipped with the reason rather than failed, and a capability that fails never stops the
// others.
package conform
