// Package session is clutch's session domain. Its root is a harness session, and each
// exchange the session carries hangs off it.
//
// The Service runs exchanges over whichever harness.Driver the composition root selects, and
// reports what it observes through an Observer rather than printing, so the direct commands
// and the narrated scenarios share it. Commands mounts the direct commands under "session":
// send, which opens or resumes a session and runs one exchange, and exchanges, which lists a
// session's recorded exchanges.
package session
