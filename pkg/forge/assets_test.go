package forge

import "testing"

// TestParsePathSpec 確認 docs/cli.md 中 "<path>[=NAME]" 分割規則的行為.
func TestParsePathSpec(t *testing.T) {
	t.Parallel()
	cases := []struct {
		spec     string
		wantPath string
		wantName string
	}{
		{"dist/app-linux=app", "dist/app-linux", "app"},
		{`D:\path\to\target.txt=beautiful_filename`, `D:\path\to\target.txt`, "beautiful_filename"},
		{"dist/checksums.txt", "dist/checksums.txt", "checksums.txt"},
		// 以最後一個 '=' 分割, 因此路徑名稱本身含 '=' 時仍可正常重新命名.
		{"weird=name/file.bin=final", "weird=name/file.bin", "final"},
		// NAME 看起來像路徑 (含分隔符) 時視為「不重新命名」:
		// 整個參數均作為純路徑處理.
		{"a=b/c", "a=b/c", "c"},
	}
	for _, tc := range cases {
		got := parsePathSpec(tc.spec)
		if got.path != tc.wantPath || got.name != tc.wantName {
			t.Errorf("parsePathSpec(%q) = {%q, %q}, want {%q, %q}",
				tc.spec, got.path, got.name, tc.wantPath, tc.wantName)
		}
	}
}

// TestMatchAssets 確認 glob 選取行為: 無 pattern 時全選、多 pattern 取聯集,
// 以及不含萬用字元的精確比對.
func TestMatchAssets(t *testing.T) {
	t.Parallel()
	assets := []asset{
		{Name: "app-linux"},
		{Name: "app-windows.exe"},
		{Name: "checksums.txt"},
		{Name: "app.sha256"},
	}
	names := func(got []asset) []string {
		out := make([]string, len(got))
		for i, a := range got {
			out[i] = a.Name
		}
		return out
	}

	if got := matchAssets(assets, nil); len(got) != 4 {
		t.Errorf("no patterns: matched %v, want all 4", names(got))
	}
	if got := matchAssets(assets, []string{"*linux*"}); len(got) != 1 || got[0].Name != "app-linux" {
		t.Errorf("'*linux*' matched %v, want [app-linux]", names(got))
	}
	if got := matchAssets(assets, []string{"checksums.txt"}); len(got) != 1 || got[0].Name != "checksums.txt" {
		t.Errorf("exact matched %v, want [checksums.txt]", names(got))
	}
	if got := matchAssets(assets, []string{"*.txt", "*.sha256"}); len(got) != 2 {
		t.Errorf("union matched %v, want 2", names(got))
	}
	if got := matchAssets(assets, []string{"*.zip"}); len(got) != 0 {
		t.Errorf("no match should be empty, got %v", names(got))
	}
}

func TestContentType(t *testing.T) {
	t.Parallel()
	if got := contentType("notes.txt"); got == "" || got[:4] != "text" {
		t.Errorf("contentType(notes.txt) = %q, want a text/* type", got)
	}
	if got := contentType("app-linux"); got != "application/octet-stream" {
		t.Errorf("contentType(no extension) = %q, want application/octet-stream", got)
	}
}
