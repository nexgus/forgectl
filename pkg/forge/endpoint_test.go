package forge

import "testing"

func TestAPIBase(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, source, host, want string
	}{
		{"github public", "github", "", "https://api.github.com"},
		{"github enterprise", "github", "https://ghe.corp.com", "https://ghe.corp.com/api/v3"},
		{"github trailing slash", "github", "https://ghe.corp.com/", "https://ghe.corp.com/api/v3"},
		{"gitlab public", "gitlab", "", "https://gitlab.com/api/v4"},
		{"gitlab self-hosted", "gitlab", "https://gitlab.corp.com", "https://gitlab.corp.com/api/v4"},
	}
	for _, tt := range tests {
		got, err := apiBase(tt.source, tt.host)
		if err != nil {
			t.Errorf("%s: apiBase error: %v", tt.name, err)
			continue
		}
		if got != tt.want {
			t.Errorf("%s: apiBase = %q, want %q", tt.name, got, tt.want)
		}
	}
	if _, err := apiBase("bitbucket", ""); err == nil {
		t.Error("apiBase should error on an unknown source")
	}
}

func TestCredentialHost(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, source, host, want string
	}{
		{"github public", "github", "", "github.com"},
		{"gitlab public", "gitlab", "", "gitlab.com"},
		{"self-hosted url", "gitlab", "https://gitlab.corp.com", "gitlab.corp.com"},
		{"bare host", "github", "ghe.corp.com", "ghe.corp.com"},
		{"url with port and path", "gitlab", "https://gitlab.corp.com:8443/base", "gitlab.corp.com"},
	}
	for _, tt := range tests {
		got, err := credentialHost(tt.source, tt.host)
		if err != nil {
			t.Errorf("%s: credentialHost error: %v", tt.name, err)
			continue
		}
		if got != tt.want {
			t.Errorf("%s: credentialHost = %q, want %q", tt.name, got, tt.want)
		}
	}
}
