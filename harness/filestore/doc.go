// Package filestore is a harness.Store kept in files: one JSON Lines file per session, named
// for the session ID, in one directory. Put appends one line per record, and Records reads
// the file back in order.
//
// It is the spike's Store. A service would keep the same records in its database, behind the
// same interface.
package filestore
