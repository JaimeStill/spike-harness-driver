//go:build !unix

package cache

// Private checks nothing where Unix ownership and permissions don't apply.
func Private(string) error { return nil }
