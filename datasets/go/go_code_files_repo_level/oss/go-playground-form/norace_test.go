//go:build !race
// +build !race

package form

// raceEnabled is false when tests are run without -race flag.
// This is only used in tests and not included in the production binary.
const raceEnabled = false
