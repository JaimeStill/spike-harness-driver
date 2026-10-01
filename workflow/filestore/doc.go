// Package filestore is a workflow.Store kept in files: one JSON Lines file per run, named for
// the run ID, in one private directory. Append adds one line per event, Events reads a run's
// file back in order, and Runs lists the files.
//
// A crash can leave a run's last line without its newline. When the line still decodes, the
// event was written whole: Events returns it, and the next Append adds the newline. When it
// doesn't, the line is torn: Events ignores it, because the runner logs again from the state it
// folds from the events before it, and the next Append cuts it. An undecodable line anywhere
// else is an error.
//
// Append takes an event only when its Seq follows the file's last, so a second process driving
// the same run fails to append rather than corrupting the log, and it syncs each event to disk.
// One state directory still belongs to one process: the store detects a second writer, and
// doesn't coordinate one.
//
// The directory is created with mode 0o700 and the files with 0o600, because a run's log holds
// its prompts and results.
//
// It is the spike's Store. A service would keep the same log in its database, behind the same
// interface.
package filestore
