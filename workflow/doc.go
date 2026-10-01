// Package workflow coordinates long-running work over harness sessions. A Workflow is a DAG of
// steps, each one exchange on one of the workflow's named sessions, and a Runner runs it: steps
// whose dependencies have ended run concurrently, a session carries one step at a time, and a
// limit bounds the exchanges in flight across every run.
//
// Each run keeps one log of Events in a Store. The log is the run's progress, the stream a
// client follows, and what a resume starts from: a run's State is a fold of its events, so a
// runner that restarts folds the log, reopens each session by the harness session ID the log
// recorded, and runs only the steps that hadn't ended. An exchange's own events, such as text
// deltas, reach the run's subscribers live and stay out of the log.
//
// The package names no adapter. The program that hosts a Runner opens each session through an
// Opener, which chooses the harness, provider, and model a SessionSpec names.
package workflow
