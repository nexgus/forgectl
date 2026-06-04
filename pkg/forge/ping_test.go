package forge

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPingGitLab(t *testing.T) {
	isolateConfig(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/version", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"version":"16.0"}`)
	})
	mux.HandleFunc("/api/v4/user", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		io.WriteString(w, `{"username":"alice","id":7}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if err := New(Config{Source: "gitlab", Host: srv.URL, Token: "good"}).Ping(); err != nil {
		t.Errorf("Ping with a valid token: %v", err)
	}
	if err := New(Config{Source: "gitlab", Host: srv.URL, Token: "bad"}).Ping(); err == nil {
		t.Error("Ping with an invalid token: want an error")
	}
	// 無 token: 連線層仍通過, 認證層略過 (匿名為合法用法).
	if err := New(Config{Source: "gitlab", Host: srv.URL}).Ping(); err != nil {
		t.Errorf("Ping anonymous (no token): %v", err)
	}
}

func TestPingGitHub(t *testing.T) {
	isolateConfig(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{}`)
	})
	mux.HandleFunc("/api/v3/user", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		io.WriteString(w, `{"login":"octocat","id":1}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if err := New(Config{Source: "github", Host: srv.URL, Token: "good"}).Ping(); err != nil {
		t.Errorf("Ping with a valid token: %v", err)
	}
	if err := New(Config{Source: "github", Host: srv.URL, Token: "bad"}).Ping(); err == nil {
		t.Error("Ping with an invalid token: want an error")
	}
}

func TestPingConnectivityFailure(t *testing.T) {
	isolateConfig(t)
	srv := httptest.NewServer(http.NewServeMux())
	url := srv.URL
	srv.Close() // url 上此時已無任何監聽程式
	if err := New(Config{Source: "gitlab", Host: url, Token: "good"}).Ping(); err == nil {
		t.Error("Ping against a dead server: want a connectivity error")
	}
}

func TestPingInsecureTLS(t *testing.T) {
	isolateConfig(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/version", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"version":"16.0"}`)
	})
	mux.HandleFunc("/api/v4/user", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"username":"alice","id":7}`)
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	// 自簽憑證在連線層會被拒絕, 除非停用 TLS 驗證.
	if err := New(Config{Source: "gitlab", Host: srv.URL, Token: "good"}).Ping(); err == nil {
		t.Error("Ping over self-signed TLS without --insecure: want an error")
	}
	if err := New(Config{Source: "gitlab", Host: srv.URL, Insecure: true, Token: "good"}).Ping(); err != nil {
		t.Errorf("Ping over self-signed TLS with --insecure: %v", err)
	}
}
