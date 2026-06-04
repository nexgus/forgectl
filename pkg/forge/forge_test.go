package forge

import (
	"strings"
	"testing"
)

// TestClientCarriesSource proves the wiring: a Client built with a given source
// reports that source from every handler. Because forge takes parameters and
// reads no global, this test is free of shared state and runs in parallel.
func TestClientCarriesSource(t *testing.T) {
	t.Parallel()
	c := New(Config{Source: "gitlab"})

	calls := map[string]func() error{
		"release list":   func() error { return c.ReleaseList("owner/repo", false) },
		"release create": func() error { return c.ReleaseCreate("owner/repo", "v1", "note", "", "") },
		"asset upload":   func() error { return c.AssetUpload("owner/repo", "v1", []string{"a.bin"}) },
		"asset download": func() error { return c.AssetDownload("owner/repo", "v1", nil, "", "", false) },
	}
	for name, call := range calls {
		err := call()
		if err == nil {
			t.Errorf("%s: expected not-implemented error", name)
			continue
		}
		if !strings.Contains(err.Error(), `source="gitlab"`) {
			t.Errorf("%s: error should carry the source from Config, got: %v", name, err)
		}
	}
}
