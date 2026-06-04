package main

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

// TestVersionArgValidation 固定 <version> 位置參數的解析期守門: 三個帶 <version>
// 的指令在送出任何請求前就拒絕非版本字串, 接受帶與不帶 "v" 前綴的寫法;
// asset download 另接受 latest, release create / asset upload 則否.
func TestVersionArgValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		// release create
		{"create v-prefixed", []string{"--source", "github", "release", "create", "r", "v1.2.3", "--note", "n"}, false},
		{"create bare semver", []string{"--source", "github", "release", "create", "r", "1.2.3", "--note", "n"}, false},
		{"create prerelease", []string{"--source", "github", "release", "create", "r", "v1.2.3-rc1", "--note", "n"}, false},
		{"create not a version", []string{"--source", "github", "release", "create", "r", "CHANGELOG.md", "--note", "n"}, true},
		{"create rejects latest", []string{"--source", "github", "release", "create", "r", "latest", "--note", "n"}, true},
		// asset upload — 漏填 version 時, 第一個 path 會落入 version 位
		{"upload bare semver", []string{"--source", "github", "asset", "upload", "r", "2.0.0", "a.bin"}, false},
		{"upload forgot version", []string{"--source", "github", "asset", "upload", "r", "dist/app.bin", "other.bin"}, true},
		{"upload rejects latest", []string{"--source", "github", "asset", "upload", "r", "latest", "a.bin"}, true},
		// asset download
		{"download semver", []string{"--source", "github", "asset", "download", "r", "v1.2.3"}, false},
		{"download latest", []string{"--source", "github", "asset", "download", "r", "latest"}, false},
		{"download not a version", []string{"--source", "github", "asset", "download", "r", "checksums.txt"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := parse(t, tt.args...)
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}
