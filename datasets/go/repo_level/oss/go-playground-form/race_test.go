//go:build race
// +build race

package form

// raceEnabled is true when tests are run with -race flag.
// This is only used in tests and not included in the production binary.
const raceEnabled = true
