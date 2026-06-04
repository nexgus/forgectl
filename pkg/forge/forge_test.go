package forge

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// TestClientDispatch proves the wiring: a Client built for a given source
// reaches that platform's API shape (GitHub under /api/v3/repos, GitLab under
// /api/v4/projects). It uses a local server so the test touches no network and
// reads no real credential file.
func TestClientDispatch(t *testing.T) {
	isolateConfig(t)

	var (
		mu  sync.Mutex
		hit []string
	)
	record := func(p string) { mu.Lock(); hit = append(hit, p); mu.Unlock() }

	// Route on the decoded path: GitLab encodes the project path as o%2Fr,
	// which http.ServeMux would not match against a decoded pattern.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/repos/o/r/releases": // GitHub
			record("github")
			io.WriteString(w, `[]`)
		case "/api/v4/projects/o/r/releases": // GitLab
			record("gitlab")
			io.WriteString(w, `[]`)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	if err := New(Config{Source: "github", Host: srv.URL, Token: "x"}).ReleaseList("o/r", false); err != nil {
		t.Fatalf("github ReleaseList: %v", err)
	}
	if err := New(Config{Source: "gitlab", Host: srv.URL, Token: "x"}).ReleaseList("o/r", false); err != nil {
		t.Fatalf("gitlab ReleaseList: %v", err)
	}

	if len(hit) != 2 || hit[0] != "github" || hit[1] != "gitlab" {
		t.Errorf("dispatch reached %v, want [github gitlab]", hit)
	}
}

// TestSplitRepo pins the GitHub owner/repo parser.
func TestSplitRepo(t *testing.T) {
	t.Parallel()
	if o, n, err := splitRepo("owner/repo"); err != nil || o != "owner" || n != "repo" {
		t.Errorf("splitRepo(owner/repo) = (%q, %q, %v)", o, n, err)
	}
	for _, bad := range []string{"single", "a/b/c", "/r", "o/", ""} {
		if _, _, err := splitRepo(bad); err == nil {
			t.Errorf("splitRepo(%q) should error", bad)
		}
	}
}
