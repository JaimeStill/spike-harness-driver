// Package app is clutch's composition root, laid out one file per layer: config.go binds the
// persistent flags, and infrastructure.go resolves the harness driver the flags name, the store
// that keeps exchange records, the skills and tools the --skills and --tools directories hold, and
// the direct model clients for the --target endpoint. workflow.go builds the workflow Runner,
// which opens each session through an Infrastructure of its own. domain.go builds the session and
// workflow domains and mounts their commands, scenarios.go mounts the narrated scenarios and their
// listing, conform.go builds the conformance suite's cells, one Infrastructure each, and mounts
// its command, and commands.go composes the mounts. app.go holds the application itself: New
// assembles the layers without I/O, and Run executes the command tree and turns its error into the
// exit code.
//
// It is the only package that names an adapter or a target: every package beneath it works
// against harness.Driver and the model clients it sets up.
package app
