package version

import "testing"

// TestFullBare checks the fallback: with no build-time injection (the situation
// under `go test`, where build.sh's -ldflags are absent), Full returns the bare
// version constant.
func TestFullBare(t *testing.T) {
	if GitCommitHash != "" || GoVersion != "" {
		t.Skip("build metadata was injected; bare-fallback case does not apply")
	}
	if got := Full(); got != String {
		t.Errorf("Full() = %q, want %q", got, String)
	}
}
