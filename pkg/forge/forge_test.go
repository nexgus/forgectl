package forge

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// TestClientDispatch 驗證路由接線: 以指定 source 建立的 Client 能對應到該 platform
// 的 API 路徑 (GitHub 在 /api/v3/repos, GitLab 在 /api/v4/projects). 測試使用
// 本地伺服器, 不觸碰網路也不讀取真實 credential 檔.
func TestClientDispatch(t *testing.T) {
	isolateConfig(t)

	var (
		mu  sync.Mutex
		hit []string
	)
	record := func(p string) { mu.Lock(); hit = append(hit, p); mu.Unlock() }

	// 依解碼後的路徑做路由: GitLab 將 project 路徑編碼為 o%2Fr,
	// http.ServeMux 無法將其與解碼後的 pattern 比對.
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

// TestSplitRepo 固定 GitHub owner/repo 解析器的行為.
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
