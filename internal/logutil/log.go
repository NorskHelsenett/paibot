package logutil

import "log"

// Verbose enables debug-level log output. Set via --verbose flag or VERBOSE_LOGS env.
var Verbose bool

// Logf logs only when verbose mode is enabled.
func Logf(format string, args ...any) {
	if Verbose {
		log.Printf(format, args...)
	}
}
