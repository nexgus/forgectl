package forge

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func ptrBool(b bool) *bool    { return &b }
func ptrInt64(n int64) *int64 { return &n }

// TestPrintReleasesText pins the human-readable listing: GitHub shows sizes and
// (draft)/(prerelease) labels; an unnamed release and an empty asset list use
// the documented placeholders.
func TestPrintReleasesText(t *testing.T) {
	t.Parallel()
	releases := []release{
		{
			Name:       "first public release",
			Tag:        "v1.2.3",
			Draft:      ptrBool(false),
			Prerelease: ptrBool(false),
			Commit:     "a1b2c3d4e5f6",
			Assets: []asset{
				{Name: "app-linux", URL: "http://x/app-linux", Size: ptrInt64(8_800_000)},
			},
		},
		{
			Name:       "",
			Tag:        "v1.3.0-rc1",
			Draft:      ptrBool(false),
			Prerelease: ptrBool(true),
			Commit:     "",
		},
	}
	var buf bytes.Buffer
	printReleasesText(&buf, releases)
	out := buf.String()

	for _, want := range []string{
		"[1] first public release",
		"release tag: v1.2.3\n",
		"commit hash: a1b2c3d\n", // short commit
		"assets (1):",
		"- app-linux (8.4 MiB)",
		"http://x/app-linux",
		"[2] (未命名)",
		"release tag: v1.3.0-rc1 (prerelease)",
		"commit hash: (未知)",
		"assets: 無",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n--- got ---\n%s", want, out)
		}
	}
}

// TestPrintReleasesTextEmpty pins the no-release message.
func TestPrintReleasesTextEmpty(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	printReleasesText(&buf, nil)
	if got := strings.TrimSpace(buf.String()); got != "沒有 release" {
		t.Errorf("empty listing = %q, want \"沒有 release\"", got)
	}
}

// TestPrintReleasesJSON pins the --json shape, including the null fields a
// platform has no value for (here GitLab: no draft/prerelease/size).
func TestPrintReleasesJSON(t *testing.T) {
	t.Parallel()
	releases := []release{{
		Name:     "v1",
		Tag:      "v1",
		Upcoming: ptrBool(true),
		Commit:   "deadbeef",
		Assets:   []asset{{Name: "app", URL: "http://x/app"}},
	}}
	var buf bytes.Buffer
	if err := printReleasesJSON(&buf, releases); err != nil {
		t.Fatalf("printReleasesJSON: %v", err)
	}

	var got []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	r := got[0]
	if r["draft"] != nil || r["prerelease"] != nil {
		t.Errorf("draft/prerelease should be null for GitLab, got %v / %v", r["draft"], r["prerelease"])
	}
	if r["upcoming"] != true {
		t.Errorf("upcoming = %v, want true", r["upcoming"])
	}
	assets := r["assets"].([]any)
	a := assets[0].(map[string]any)
	if a["size"] != nil {
		t.Errorf("size should be null for GitLab links, got %v", a["size"])
	}
	if a["name"] != "app" {
		t.Errorf("asset name = %v, want app", a["name"])
	}
}

func TestHumanSize(t *testing.T) {
	t.Parallel()
	cases := map[int64]string{
		0:          "0 B",
		124:        "124 B",
		1024:       "1.0 KiB",
		8_800_000:  "8.4 MiB",
		1073741824: "1.0 GiB",
	}
	for n, want := range cases {
		if got := humanSize(n); got != want {
			t.Errorf("humanSize(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestShortCommit(t *testing.T) {
	t.Parallel()
	if got := shortCommit("a1b2c3d4e5f6"); got != "a1b2c3d" {
		t.Errorf("shortCommit long = %q, want a1b2c3d", got)
	}
	if got := shortCommit("abc"); got != "abc" {
		t.Errorf("shortCommit short = %q, want abc", got)
	}
}
