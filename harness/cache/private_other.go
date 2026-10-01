//go:build !unix

package cache

// private checks nothing where Unix ownership and permissions don't apply.
func private(string) error { return nil }
