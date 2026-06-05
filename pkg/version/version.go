// Package version holds the application version and the build metadata that
// build.sh injects at compile time.
package version

import "fmt"

// String is the human-facing application version.
const String = "0.2.1"

// GitCommitHash, GoVersion, and BuildDate are injected at build time by
// build.sh through -ldflags. They stay empty when the binary is run directly
// (for example with `go run`). BuildDate is the build machine's local time in
// ISO 8601 with a timezone offset (for example 2026-06-04T08:19:01+0800).
var (
	GitCommitHash string
	GoVersion     string
	BuildDate     string
)

// Full returns the version string shown by the --version flag. It appends the
// git commit hash and the Go compiler version when they were injected at build
// time, and falls back to the bare version otherwise.
func Full() string {
	v := String
	if GitCommitHash != "" {
		v = fmt.Sprintf("%s+%s", v, GitCommitHash)
	}
	if GoVersion != "" {
		v = fmt.Sprintf("%s (%s)", v, GoVersion)
	}
	return v
}
