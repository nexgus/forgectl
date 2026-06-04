package main

import (
	"strings"
	"testing"

	"github.com/alecthomas/kong"
)

// parse runs the real grammar against args, binding into a fresh CLI so each
// test stays independent of any shared state and can run in parallel.
func parse(t *testing.T, args ...string) (*CLI, *kong.Context, error) {
	t.Helper()
	var c CLI
	parser, err := newParser(&c)
	if err != nil {
		t.Fatalf("newParser: %v", err)
	}
	ctx, err := parser.Parse(args)
	return &c, ctx, err
}

func TestReleaseList(t *testing.T) {
	t.Parallel()
	c, ctx, err := parse(t, "--source", "github", "release", "list", "owner/repo", "--json")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.Source != "github" {
		t.Errorf("Source = %q, want github", c.Source)
	}
	if c.Release.List.Repo != "owner/repo" {
		t.Errorf("Repo = %q, want owner/repo", c.Release.List.Repo)
	}
	if !c.Release.List.JSON {
		t.Error("JSON = false, want true")
	}
	if got := ctx.Command(); got != "release list <repo>" {
		t.Errorf("Command() = %q, want %q", got, "release list <repo>")
	}
}

func TestSourceValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{"missing", []string{"release", "list", "r"}, true},
		{"invalid enum", []string{"--source", "bitbucket", "release", "list", "r"}, true},
		{"github", []string{"--source", "github", "release", "list", "r"}, false},
		{"gitlab", []string{"--source", "gitlab", "release", "list", "r"}, false},
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

// TestReleaseCreateNoteXor pins the xor:"note" constraint: exactly one of
// --note / --note-file is required.
func TestReleaseCreateNoteXor(t *testing.T) {
	t.Parallel()
	base := []string{"--source", "github", "release", "create", "r", "v1"}
	tests := []struct {
		name    string
		extra   []string
		wantErr bool
	}{
		{"note only", []string{"--note", "hi"}, false},
		{"note-file only", []string{"--note-file", "/tmp/notes.md"}, false},
		{"neither", nil, true},
		{"both", []string{"--note", "hi", "--note-file", "/tmp/notes.md"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			args := append(append([]string{}, base...), tt.extra...)
			_, _, err := parse(t, args...)
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestAssetUploadPaths(t *testing.T) {
	t.Parallel()
	c, ctx, err := parse(t, "--source", "github", "asset", "upload", "r", "v1", "a.bin=alpha", "b.bin")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []string{"a.bin=alpha", "b.bin"}
	if got := c.Asset.Upload.Paths; !equal(got, want) {
		t.Errorf("Paths = %v, want %v", got, want)
	}
	if got := ctx.Command(); got != "asset upload <repo> <version> <path>" {
		t.Errorf("Command() = %q", got)
	}
}

// TestAssetDownloadOptionalPattern pins that the variadic pattern is optional.
func TestAssetDownloadOptionalPattern(t *testing.T) {
	t.Parallel()
	t.Run("no pattern", func(t *testing.T) {
		t.Parallel()
		c, ctx, err := parse(t, "--source", "github", "asset", "download", "r", "v1")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if len(c.Asset.Download.Patterns) != 0 {
			t.Errorf("Patterns = %v, want empty", c.Asset.Download.Patterns)
		}
		if got := ctx.Command(); got != "asset download <repo> <version>" {
			t.Errorf("Command() = %q", got)
		}
	})
	t.Run("with patterns", func(t *testing.T) {
		t.Parallel()
		c, _, err := parse(t, "--source", "github", "asset", "download", "r", "v1", "*.bin", "*.zip")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if got := c.Asset.Download.Patterns; !equal(got, []string{"*.bin", "*.zip"}) {
			t.Errorf("Patterns = %v, want [*.bin *.zip]", got)
		}
	})
}

// TestRunDispatch checks the wiring from each command's Run through to forge:
// every Run builds a client from the globals and reaches the matching handler,
// which (until implemented) reports the source it was given.
func TestRunDispatch(t *testing.T) {
	t.Parallel()
	g := &Globals{Source: "github"}
	runs := map[string]func() error{
		"release list":   func() error { return (&ReleaseListCmd{Repo: "r"}).Run(g) },
		"release create": func() error { return (&ReleaseCreateCmd{Repo: "r", Version: "v1", Note: "n"}).Run(g) },
		"asset upload":   func() error { return (&AssetUploadCmd{Repo: "r", Version: "v1", Paths: []string{"a"}}).Run(g) },
		"asset download": func() error { return (&AssetDownloadCmd{Repo: "r", Version: "v1"}).Run(g) },
	}
	for name, run := range runs {
		err := run()
		if err == nil {
			t.Errorf("%s: expected not-implemented error", name)
			continue
		}
		if !strings.Contains(err.Error(), `source="github"`) {
			t.Errorf("%s: error should carry the source through to forge, got: %v", name, err)
		}
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
