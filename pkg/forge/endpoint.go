package forge

import (
	"fmt"
	"net/url"
	"strings"
)

// apiBase derives the REST API base URL from the source and the optional
// self-hosted host, following the convention table in docs/cli.md:
//
//	source  self-hosted (--host H)   public (no --host)
//	gitlab  H/api/v4                 https://gitlab.com/api/v4
//	github  H/api/v3                 https://api.github.com
//
// GitHub is asymmetric: the public site is api.github.com, while a self-hosted
// GitHub Enterprise instance serves the API under {host}/api/v3.
func apiBase(source, host string) (string, error) {
	host = strings.TrimRight(strings.TrimSpace(host), "/")
	switch source {
	case "gitlab":
		if host == "" {
			return "https://gitlab.com/api/v4", nil
		}
		return host + "/api/v4", nil
	case "github":
		if host == "" {
			return "https://api.github.com", nil
		}
		return host + "/api/v3", nil
	default:
		return "", fmt.Errorf("unknown source %q", source)
	}
}

// credentialHost returns the host name used as the credential-file key for this
// connection: the public site's host when --host is omitted, otherwise the host
// component of --host. Per docs/credential.md the credential file is keyed by
// host name (for example "github.com" or "gitlab.mycorp.com").
func credentialHost(source, host string) (string, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		switch source {
		case "gitlab":
			return "gitlab.com", nil
		case "github":
			return "github.com", nil
		default:
			return "", fmt.Errorf("unknown source %q", source)
		}
	}
	// --host is a base URL such as https://gitlab.mycorp.com, but tolerate a
	// bare host without a scheme as well; either way we want the host name.
	raw := host
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("invalid --host %q", host)
	}
	return u.Hostname(), nil
}
