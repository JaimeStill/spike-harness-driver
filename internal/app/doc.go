// Package app is clutch's composition root, laid out one file per layer: config.go binds the
// persistent flags, infrastructure.go resolves the harness driver the flags name and the store
// that keeps exchange records, domain.go builds the session domain and mounts its commands,
// scenarios.go mounts the narrated scenarios and their listing, and commands.go composes the
// mounts. app.go holds the application itself: New assembles the layers without I/O, and Run
// executes the command tree and turns its error into the exit code.
//
// It is the only package that names an adapter: every package beneath it works against
// harness.Driver.
package app
