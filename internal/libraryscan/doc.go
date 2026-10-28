// Package libraryscan coordinates background discovery without media processing.
// Run owns the automatic scheduler; Start may also launch explicit rounds.
// Close cancels both sources of work and waits for every in-flight scan.
package libraryscan
