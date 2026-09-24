// Package harnesstest is a scripted harness for the tests of packages that drive one through
// harness.Driver, following the standard library's httptest and fstest naming. Nothing in it
// is API.
//
// A Driver opens sessions over a Connection that answers a prompt with Reply, one text delta
// per word, and ends the run with a stop. A prompt that contains the Driver's Stream marker
// instead streams text deltas until it is cancelled, then ends the run as aborted, the way Pi
// does.
package harnesstest
