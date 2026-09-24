// Package output renders what clutch observes: a harness's normalized events, an exchange's
// result, and the error a command ends with. The session commands and the scenarios share it,
// so an event reads the same wherever it is printed.
//
// Results go to stdout and errors to stderr. An event line leads with the last eight
// characters of its session and exchange IDs, which differ between IDs made in the same
// millisecond, then its sequence number, kind, and detail.
package output
