package forge

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// glServer builds a GitLab-shaped test server and a platform pointed at it. The
// project path "o/r" is URL-encoded to "o%2Fr" in requests; http.ServeMux does
// not match that against a decoded pattern, so routing here is a single handler
// keyed on the decoded r.URL.Path (e.g. "/api/v4/projects/o/r/releases").
func glServer(t *testing.T, routes map[string]http.HandlerFunc) *gitlabPlatform {
	t.Helper()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fn, ok := routes[r.URL.Path]; ok {
			fn(w, r)
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	g, err := newGitLabPlatform(newHTTPClient(false), srv.URL+"/api/v4", "tok", "o/r")
	if err != nil {
		t.Fatalf("newGitLabPlatform: %v", err)
	}
	return g
}

func TestGitLabListReleases(t *testing.T) {
	t.Parallel()
	g := glServer(t, map[string]http.HandlerFunc{
		"/api/v4/projects/o/r/releases": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `[{"name":"v1","tag_name":"v1","upcoming_release":false,
				"assets":{"links":[{"name":"app","url":"http://x/app"}]}}]`)
		},
		"/api/v4/projects/o/r/repository/tags/v1": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"commit":{"id":"deadbeef00"}}`)
		},
	})

	releases, err := g.listReleases()
	if err != nil {
		t.Fatalf("listReleases: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("len = %d, want 1", len(releases))
	}
	r := releases[0]
	if r.Tag != "v1" || r.Commit != "deadbeef00" {
		t.Errorf("(tag, commit) = (%q, %q), want (v1, deadbeef00)", r.Tag, r.Commit)
	}
	if r.Upcoming == nil || *r.Upcoming {
		t.Errorf("upcoming = %v, want non-nil false", r.Upcoming)
	}
	if r.Draft != nil || r.Prerelease != nil {
		t.Errorf("draft/prerelease should be nil for GitLab, got %v / %v", r.Draft, r.Prerelease)
	}
	if len(r.Assets) != 1 || r.Assets[0].Size != nil {
		t.Errorf("GitLab asset should have nil size: %+v", r.Assets)
	}
}

func TestGitLabCreateReleaseWithLinks(t *testing.T) {
	t.Parallel()
	var (
		posted map[string]any
		linked map[string]any
	)
	g := glServer(t, map[string]http.HandlerFunc{
		"/api/v4/projects/o/r/releases/v1": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound) // releaseExists -> false
		},
		"/api/v4/projects/o/r/repository/tags/v1": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"commit":{"id":"abc"}}`) // tag exists
		},
		"/api/v4/projects/o/r/releases": func(w http.ResponseWriter, r *http.Request) {
			posted = decodeBody(t, r)
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, `{}`)
		},
		"/api/v4/projects/o/r/packages": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `[{"id":11,"name":"r","version":"v1","package_type":"generic"}]`)
		},
		"/api/v4/projects/o/r/packages/11/package_files": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `[{"id":18,"file_name":"app"}]`)
		},
		"/api/v4/projects/o/r/releases/v1/assets/links": func(w http.ResponseWriter, r *http.Request) {
			linked = decodeBody(t, r)
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, `{}`)
		},
	})

	if err := g.createRelease("v1", "note", ""); err != nil {
		t.Fatalf("createRelease: %v", err)
	}
	if posted["tag_name"] != "v1" || posted["description"] != "note" {
		t.Errorf("POST body = %v, want tag_name v1 and description note", posted)
	}
	if _, ok := posted["ref"]; ok {
		t.Errorf("ref should be absent when the tag exists, got %v", posted["ref"])
	}
	if linked["name"] != "app" {
		t.Errorf("link name = %v, want app", linked["name"])
	}
	if url, _ := linked["url"].(string); !strings.Contains(url, "/packages/generic/r/v1/app") {
		t.Errorf("link url = %v, want the by-name package URL", linked["url"])
	}
}

func TestGitLabCreateReleaseAlreadyExists(t *testing.T) {
	t.Parallel()
	g := glServer(t, map[string]http.HandlerFunc{
		"/api/v4/projects/o/r/releases/v1": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"tag_name":"v1"}`) // exists
		},
	})

	err := g.createRelease("v1", "note", "")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("err = %v, want 'already exists'", err)
	}
}

// TestGitLabUploadOverwrite proves the delete-then-upload order, and that an
// existing release gets a link.
func TestGitLabUploadOverwrite(t *testing.T) {
	t.Parallel()
	var (
		mu     sync.Mutex
		order  []string
		linked bool
	)
	record := func(s string) { mu.Lock(); order = append(order, s); mu.Unlock() }

	g := glServer(t, map[string]http.HandlerFunc{
		"/api/v4/projects/o/r/releases/v1": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"tag_name":"v1"}`) // release exists -> link on upload
		},
		"/api/v4/projects/o/r/packages": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `[{"id":11,"name":"r","version":"v1","package_type":"generic"}]`)
		},
		"/api/v4/projects/o/r/packages/11/package_files": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `[{"id":18,"file_name":"app"}]`)
		},
		"/api/v4/projects/o/r/packages/11/package_files/18": func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				record("delete")
				w.WriteHeader(http.StatusNoContent)
			}
		},
		"/api/v4/projects/o/r/packages/generic/r/v1/app": func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPut {
				record("upload")
				w.WriteHeader(http.StatusCreated)
				io.WriteString(w, `{}`)
			}
		},
		"/api/v4/projects/o/r/releases/v1/assets/links": func(w http.ResponseWriter, r *http.Request) {
			linked = true
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, `{}`)
		},
	})

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
	if !linked {
		t.Error("an existing release should get an asset link on upload")
	}
}

func TestGitLabDownload(t *testing.T) {
	t.Parallel()
	g := glServer(t, map[string]http.HandlerFunc{
		"/api/v4/projects/o/r/releases/v1": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"tag_name":"v1","assets":{"links":[{"name":"app","url":"http://`+r.Host+`/dl/app"}]}}`)
		},
		"/dl/app": func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("PRIVATE-TOKEN") != "tok" {
				t.Errorf("download missing token, headers: %v", r.Header)
			}
			io.WriteString(w, "xyz")
		},
	})

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
	if buf.String() != "xyz" {
		t.Errorf("downloaded %q, want xyz", buf.String())
	}
}
