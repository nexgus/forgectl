package forge

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// decodeBody reads a request's JSON body into a map for assertions.
func decodeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		t.Fatalf("decoding request body: %v", err)
	}
	return m
}

// ghServer builds a GitHub-shaped test server and a platform pointed at it. The
// base mirrors a self-hosted instance (apiBase appends /api/v3).
func ghServer(t *testing.T, mux *http.ServeMux) *githubPlatform {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return newGitHubPlatform(newHTTPClient(false), srv.URL+"/api/v3", "tok", "o", "r")
}

func TestGitHubListReleases(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/o/r/releases", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[{"id":1,"name":"first","tag_name":"v1.2.3","draft":false,"prerelease":false,
			"assets":[{"id":5,"name":"app-linux","size":8800000,"browser_download_url":"http://x/app-linux"}]}]`)
	})
	mux.HandleFunc("/api/v3/repos/o/r/commits/v1.2.3", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"sha":"a1b2c3d4e5f6"}`)
	})
	g := ghServer(t, mux)

	releases, err := g.listReleases()
	if err != nil {
		t.Fatalf("listReleases: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("len = %d, want 1", len(releases))
	}
	r := releases[0]
	if r.Tag != "v1.2.3" || r.Commit != "a1b2c3d4e5f6" {
		t.Errorf("(tag, commit) = (%q, %q), want (v1.2.3, a1b2c3d4e5f6)", r.Tag, r.Commit)
	}
	if r.Draft == nil || *r.Draft {
		t.Errorf("draft = %v, want non-nil false", r.Draft)
	}
	if len(r.Assets) != 1 || r.Assets[0].Size == nil || *r.Assets[0].Size != 8800000 {
		t.Errorf("asset size not carried: %+v", r.Assets)
	}
}

func TestGitHubCreateReleaseNew(t *testing.T) {
	t.Parallel()
	var posted map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/o/r/releases", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			io.WriteString(w, `[]`) // no existing release
			return
		}
		posted = decodeBody(t, r)
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"id":1}`)
	})
	mux.HandleFunc("/api/v3/repos/o/r/git/ref/tags/v1.2.3", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"ref":"refs/tags/v1.2.3"}`) // tag exists
	})
	g := ghServer(t, mux)

	if err := g.createRelease("v1.2.3", "the note", ""); err != nil {
		t.Fatalf("createRelease: %v", err)
	}
	if posted["tag_name"] != "v1.2.3" || posted["body"] != "the note" {
		t.Errorf("POST body = %v, want tag_name v1.2.3 and body 'the note'", posted)
	}
	if _, ok := posted["target_commitish"]; ok {
		t.Errorf("target_commitish should be absent when the tag exists, got %v", posted["target_commitish"])
	}
}

func TestGitHubCreateReleaseAlreadyPublished(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/o/r/releases", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[{"id":1,"tag_name":"v1.2.3","draft":false}]`)
	})
	g := ghServer(t, mux)

	err := g.createRelease("v1.2.3", "note", "")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("createRelease over a published release: err = %v, want 'already exists'", err)
	}
}

func TestGitHubCreateReleaseTagMissingNeedsCommit(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/o/r/releases", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[]`)
	})
	mux.HandleFunc("/api/v3/repos/o/r/git/ref/tags/v9", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	g := ghServer(t, mux)

	err := g.createRelease("v9", "note", "")
	if err == nil || !strings.Contains(err.Error(), "--commit") {
		t.Errorf("missing tag without --commit: err = %v, want a --commit hint", err)
	}
}

func TestGitHubCreateReleasePublishDraft(t *testing.T) {
	t.Parallel()
	var patched map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/o/r/releases", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[{"id":42,"tag_name":"v1","draft":true}]`)
	})
	mux.HandleFunc("/api/v3/repos/o/r/git/ref/tags/v1", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"ref":"refs/tags/v1"}`) // tag exists
	})
	mux.HandleFunc("/api/v3/repos/o/r/releases/42", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("method = %s, want PATCH", r.Method)
		}
		patched = decodeBody(t, r)
		io.WriteString(w, `{"id":42}`)
	})
	g := ghServer(t, mux)

	if err := g.createRelease("v1", "release note", ""); err != nil {
		t.Fatalf("createRelease (publish draft): %v", err)
	}
	if patched["draft"] != false || patched["body"] != "release note" {
		t.Errorf("PATCH body = %v, want draft false and the note", patched)
	}
}

// TestGitHubUploadOverwrite proves the delete-then-upload order: a same-name
// asset is removed before the new upload.
func TestGitHubUploadOverwrite(t *testing.T) {
	t.Parallel()
	var (
		mu    sync.Mutex
		order []string
	)
	record := func(s string) { mu.Lock(); order = append(order, s); mu.Unlock() }

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/o/r/releases", func(w http.ResponseWriter, r *http.Request) {
		// findReleaseByTag: a draft already staging assets.
		io.WriteString(w, `[{"id":7,"tag_name":"v1","draft":true,
			"upload_url":"`+uploadsBase(r)+`/uploads/repos/o/r/releases/7/assets{?name,label}"}]`)
	})
	mux.HandleFunc("/api/v3/repos/o/r/releases/7/assets", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[{"id":99,"name":"app"}]`) // existing same-name asset
	})
	mux.HandleFunc("/api/v3/repos/o/r/releases/assets/99", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			record("delete")
			w.WriteHeader(http.StatusNoContent)
		}
	})
	mux.HandleFunc("/uploads/repos/o/r/releases/7/assets", func(w http.ResponseWriter, r *http.Request) {
		record("upload")
		if got := r.URL.Query().Get("name"); got != "app" {
			t.Errorf("upload name = %q, want app", got)
		}
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"id":100}`)
	})
	g := ghServer(t, mux)

	up, err := g.newUploader("v1")
	if err != nil {
		t.Fatalf("newUploader: %v", err)
	}
	if err := up.upload(localAsset{path: tempFile(t, "data"), name: "app"}); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if len(order) != 2 || order[0] != "delete" || order[1] != "upload" {
		t.Errorf("order = %v, want [delete upload]", order)
	}
}

func TestGitHubDownload(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/o/r/releases/tags/v1", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"id":1,"assets":[{"id":5,"name":"app","size":3,"browser_download_url":"http://x/app"}]}`)
	})
	mux.HandleFunc("/api/v3/repos/o/r/releases/assets/5", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/octet-stream" {
			t.Errorf("Accept = %q, want application/octet-stream", r.Header.Get("Accept"))
		}
		io.WriteString(w, "abc")
	})
	g := ghServer(t, mux)

	assets, err := g.findReleaseAssets("v1")
	if err != nil {
		t.Fatalf("findReleaseAssets: %v", err)
	}
	if len(assets) != 1 || assets[0].Name != "app" {
		t.Fatalf("assets = %+v, want one named app", assets)
	}
	var buf bytes.Buffer
	if err := g.download(assets[0], &buf); err != nil {
		t.Fatalf("download: %v", err)
	}
	if buf.String() != "abc" {
		t.Errorf("downloaded %q, want abc", buf.String())
	}
}

func TestGitHubDownloadReleaseNotFound(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/o/r/releases/tags/v9", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	g := ghServer(t, mux)
	if _, err := g.findReleaseAssets("v9"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v, want 'not found'", err)
	}
}

// uploadsBase returns the scheme://host of the test request, so the synthetic
// upload_url points back at the same test server.
func uploadsBase(r *http.Request) string {
	return "http://" + r.Host
}

func tempFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "asset.bin")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}
