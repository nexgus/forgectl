package forge

import "testing"

// TestParsePathSpec pins the "<path>[=NAME]" splitting rule of docs/cli.md.
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
		// The last '=' splits, so a path whose name contains '=' still renames.
		{"weird=name/file.bin=final", "weird=name/file.bin", "final"},
		// A NAME that looks like a path (has a separator) means "no rename":
		// the whole argument is a plain path.
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

// TestMatchAssets pins glob selection: no patterns = all, union of patterns,
// and exact (wildcard-free) matching.
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
