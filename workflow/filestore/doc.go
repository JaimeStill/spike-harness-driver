// Package filestore is a workflow.Store kept in files: one JSON Lines file per run, named for
// the run ID, in one private directory. Append adds one line per event, Events reads a run's
// file back in order, and Runs lists the files.
//
// A crash can leave a run's last line torn: cut off before its newline. Events ignores an
// undecodable last line that has no trailing newline, because the runner logs again from the
// state it folds from the events before it. Append trims the torn tail before it adds the next
// line. An undecodable line anywhere else is an error.
//
// The directory is created with mode 0o700 and the files with 0o600, because a run's log holds
// its prompts and results.
//
// It is the spike's Store. A service would keep the same log in its database, behind the same
// interface.
package filestore
