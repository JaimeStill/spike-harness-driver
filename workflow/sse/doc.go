// Package sse serves a workflow run's events to a client as a Server-Sent Events stream.
//
// The stream replays the run's log after the client's cursor and then follows the run live, so a
// client that connects late or reconnects sees the same events as one that never left. Only
// logged events carry an id, their Seq, so the Last-Event-ID that a browser sends on
// reconnecting always names a logged event and resumes exactly after the last one the client
// saw. The exchange events a step streams while it runs, such as text deltas, are live only and
// carry no id. A client that was disconnected for some of them isn't replayed them, because they
// were never logged; the result in the step's logged step_ended event stands for them.
//
// The package uses only net/http and a workflow.Runner, so any server can mount Stream on any
// route.
package sse
