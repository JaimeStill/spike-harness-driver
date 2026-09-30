// Package app is clutch's composition root, laid out one file per layer: config.go binds the
// persistent flags, infrastructure.go resolves the harness driver the flags name, the store
// that keeps exchange records, the skills and tools the --skills and --tools directories hold,
// and the direct model clients for the --target endpoint, domain.go builds the session domain and mounts its commands, scenarios.go mounts the
// narrated scenarios and their listing, and commands.go composes the mounts. app.go holds the
// application itself: New assembles the layers without I/O, and Run executes the command tree
// and turns its error into the exit code.
//
// It is the only package that names an adapter or a target: every package beneath it works
// against harness.Driver and the model clients it sets up.
package app
