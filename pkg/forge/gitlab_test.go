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

// glServer 建立一個模擬 GitLab 結構的測試伺服器, 並回傳指向該伺服器的 platform.
// project 路徑 "o/r" 在請求中被 URL-encode 為 "o%2Fr"; http.ServeMux 無法將其
// 與 decoded 路徑進行比對, 因此這裡使用單一 handler, 以 decoded 的 r.URL.Path
// (例如 "/api/v4/projects/o/r/releases") 作為路由鍵.
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
	if err == nil || !strings.Contains(err.Error(), "已存在") {
		t.Errorf("err = %v, want 'already exists'", err)
	}
}

// TestGitLabUploadOverwrite 驗證先刪後傳的順序, 以及已存在的 release 在上傳時
// 會取得 asset link.
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

// TestGitLabDownloadLoginRedirect 模擬認證失敗時下載被重導至登入頁的情形:
// 應回報錯誤, 而非把整頁登入 HTML 當成 asset 寫出.
func TestGitLabDownloadLoginRedirect(t *testing.T) {
	t.Parallel()
	g := glServer(t, map[string]http.HandlerFunc{
		"/api/v4/projects/o/r/releases/v1": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"tag_name":"v1","assets":{"links":[{"name":"app","url":"http://`+r.Host+`/dl/app"}]}}`)
		},
		"/dl/app": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/users/sign_in", http.StatusFound)
		},
		"/users/sign_in": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, "<!DOCTYPE html><title>Sign in · GitLab</title>")
		},
	})

	assets, err := g.findReleaseAssets("v1")
	if err != nil {
		t.Fatalf("findReleaseAssets: %v", err)
	}
	var buf bytes.Buffer
	err = g.download(assets[0], &buf)
	if err == nil {
		t.Fatalf("download succeeded, want login-redirect error; wrote %q", buf.String())
	}
	if !strings.Contains(err.Error(), "登入頁") {
		t.Errorf("error = %v, want mention of 登入頁", err)
	}
}

// TestGitLabDownloadPackageLink 重現實際情境: GitLab 把 link_type=package 的 url
// 正規化成 web permalink (/-/package_files/:id/download), 該路由帶 token 仍導向登入頁.
// 下載應改打 by-name generic package API 端點 (接受 PRIVATE-TOKEN), 取得真正的檔案.
func TestGitLabDownloadPackageLink(t *testing.T) {
	t.Parallel()
	g := glServer(t, map[string]http.HandlerFunc{
		"/api/v4/projects/o/r/releases/v1": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"tag_name":"v1","assets":{"links":[{
				"name":"app.whl","link_type":"package",
				"url":"http://`+r.Host+`/o/r/-/package_files/67/download",
				"direct_asset_url":"http://`+r.Host+`/o/r/-/package_files/67/download"}]}}`)
		},
		// web permalink: forgectl 若誤用此路由將取得登入頁 (測試會因此失敗).
		"/o/r/-/package_files/67/download": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/users/sign_in", http.StatusFound)
		},
		"/users/sign_in": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, "<!DOCTYPE html><title>Sign in</title>")
		},
		// by-name generic package API 端點: pkgName=r (repo base), version=tag, name=檔名.
		"/api/v4/projects/o/r/packages/generic/r/v1/app.whl": func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("PRIVATE-TOKEN") != "tok" {
				t.Errorf("API download missing token, headers: %v", r.Header)
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			io.WriteString(w, "PK\x03\x04realwheel")
		},
	})

	assets, err := g.findReleaseAssets("v1")
	if err != nil {
		t.Fatalf("findReleaseAssets: %v", err)
	}
	if len(assets) != 1 {
		t.Fatalf("assets = %+v, want one", assets)
	}
	var buf bytes.Buffer
	if err := g.download(assets[0], &buf); err != nil {
		t.Fatalf("download: %v", err)
	}
	if buf.String() != "PK\x03\x04realwheel" {
		t.Errorf("downloaded %q, want real wheel bytes (did it hit the web permalink?)", buf.String())
	}
}
