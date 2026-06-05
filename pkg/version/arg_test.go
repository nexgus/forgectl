package version

import "testing"

// TestIsSemver 固定 isSemver 的判定: 接受帶與不帶 "v" 前綴的合法版本,
// 拒絕看起來不像版本的字串 (常見的漏填 <version> 情境).
func TestIsSemver(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"v1.2.3":       true,
		"1.2.3":        true,
		"v1":           true,
		"1":            true,
		"v1.2.3-rc1":   true,
		"1.2.3-rc.1":   true,
		"":             false,
		"v":            false,
		"latest":       false,
		"vendor":       false,
		"CHANGELOG.md": false,
		"dist/app.bin": false,
	}
	for in, want := range cases {
		if got := isSemver(in); got != want {
			t.Errorf("isSemver(%q) = %v, want %v", in, got, want)
		}
	}
}
