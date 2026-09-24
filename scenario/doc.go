// Package scenario runs clutch's narrated scenarios. Each scenario shows one capability of the
// harness driver on its own: it states what must be in place, then runs its steps in order,
// saying what each is about to do before doing it and reporting what it observed.
//
// A Scenario's Steps builds the steps of one run, so the steps can share that run's state,
// such as an open session, and the cleanup that releases it whatever step fails. The
// scenarios are defined beside the runner: exchange.go and cancel.go.
package scenario
