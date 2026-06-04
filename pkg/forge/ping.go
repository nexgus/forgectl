package forge

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// pingTimeout bounds each HTTP request that ping makes.
const pingTimeout = 30 * time.Second

// Ping verifies the remote settings. It resolves the connection target and
// credentials, then runs a layered check:
//
//  1. Connectivity: a request that needs no token, so it validates --host,
//     --insecure, and TLS even when no credentials are present. Any HTTP
//     response (even an error status) proves the API was reached; only a
//     transport-level failure counts as a connectivity failure.
//  2. Authentication: when a token is resolved, a request to the platform's
//     current-user endpoint, which confirms the token and reports the
//     authenticated identity. Skipped when no token is resolved (anonymous use
//     is valid).
//
// The resolved settings and each layer's result are printed to stdout; Ping
// returns an error (non-zero exit) when a layer fails.
func (c *Client) Ping() error {
	base, err := apiBase(c.cfg.Source, c.cfg.Host)
	if err != nil {
		return err
	}
	host, err := credentialHost(c.cfg.Source, c.cfg.Host)
	if err != nil {
		return err
	}
	a, err := resolveAuth(c.cfg, host)
	if err != nil {
		return err
	}

	emitWarnings(a, c.cfg.Insecure)

	c.printSettings(base, host, a)

	client := &http.Client{Timeout: pingTimeout}
	if c.cfg.Insecure {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}

	// Layer 1: connectivity.
	connURL := base + connectivityPath(c.cfg.Source)
	resp, err := doGet(client, connURL, nil)
	if err != nil {
		return fmt.Errorf("connectivity check failed for %s: %w", connURL, err)
	}
	resp.Body.Close()
	fmt.Println("Connectivity: OK")

	// Layer 2: authentication.
	if a.Token == "" {
		fmt.Println("Auth:         skipped (no credentials resolved)")
		return nil
	}
	identity, err := c.checkAuth(client, base, a.Token)
	if err != nil {
		return err
	}
	fmt.Printf("Auth:         OK - authenticated as %s\n", identity)
	return nil
}

// printSettings writes the resolved connection settings to stdout. It prints
// where the token and user came from but never the token value itself.
func (c *Client) printSettings(base, host string, a auth) {
	site := "public"
	if c.cfg.Host != "" {
		site = "self-hosted"
	}
	tlsVerify := "on"
	if c.cfg.Insecure {
		tlsVerify = "off (--insecure)"
	}
	line := func(label, value string) { fmt.Printf("%-14s%s\n", label, value) }
	line("Source:", c.cfg.Source)
	line("Host:", fmt.Sprintf("%s (%s)", host, site))
	line("API base:", base)
	line("TLS verify:", tlsVerify)
	line("Token:", a.TokenSource)
	if a.User != "" {
		line("User:", fmt.Sprintf("%s (%s)", a.User, a.UserSource))
	} else {
		line("User:", a.UserSource)
	}
	fmt.Println()
}

// connectivityPath is the path appended to the API base for the connectivity
// probe: a no-auth endpoint on each platform.
func connectivityPath(source string) string {
	switch source {
	case "gitlab":
		return "/version"
	default: // github: the API root responds without authentication
		return ""
	}
}

// checkAuth calls the platform's current-user endpoint with the token and
// returns a human-readable identity on success. A non-2xx response means the
// credentials are not valid for this endpoint; on GitLab this includes deploy
// tokens, which cannot access /user.
func (c *Client) checkAuth(client *http.Client, base, token string) (string, error) {
	url := base + "/user"
	var headers map[string]string
	switch c.cfg.Source {
	case "github":
		headers = map[string]string{
			"Authorization":        "Bearer " + token,
			"Accept":               "application/vnd.github+json",
			"X-GitHub-Api-Version": "2022-11-28",
		}
	case "gitlab":
		headers = map[string]string{"PRIVATE-TOKEN": token}
	}

	resp, err := doGet(client, url, headers)
	if err != nil {
		return "", fmt.Errorf("authentication check failed for %s: %w", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", c.authError(resp.StatusCode)
	}
	return parseIdentity(c.cfg.Source, body)
}

// authError turns a non-2xx status from the current-user endpoint into an
// actionable error.
func (c *Client) authError(status int) error {
	switch status {
	case http.StatusUnauthorized:
		if c.cfg.Source == "gitlab" {
			return fmt.Errorf("authentication failed: 401 Unauthorized; the token is invalid or expired, or it is a deploy token (deploy tokens cannot access /user - validate them with a real asset operation)")
		}
		return fmt.Errorf("authentication failed: 401 Unauthorized; the token is invalid or expired")
	case http.StatusForbidden:
		return fmt.Errorf("authentication failed: 403 Forbidden; the token lacks the required scope, or the request was rate-limited")
	default:
		return fmt.Errorf("authentication failed: unexpected status %d from /user", status)
	}
}

// parseIdentity extracts the authenticated identity from a current-user
// response body: the login on GitHub, the username on GitLab.
func parseIdentity(source string, body []byte) (string, error) {
	switch source {
	case "github":
		var u struct {
			Login string `json:"login"`
			ID    int64  `json:"id"`
		}
		if err := json.Unmarshal(body, &u); err != nil || u.Login == "" {
			return "", fmt.Errorf("authentication succeeded but the user response could not be parsed")
		}
		return fmt.Sprintf("%q (id %d)", u.Login, u.ID), nil
	case "gitlab":
		var u struct {
			Username string `json:"username"`
			ID       int64  `json:"id"`
		}
		if err := json.Unmarshal(body, &u); err != nil || u.Username == "" {
			return "", fmt.Errorf("authentication succeeded but the user response could not be parsed")
		}
		return fmt.Sprintf("%q (id %d)", u.Username, u.ID), nil
	default:
		return "", fmt.Errorf("unknown source %q", source)
	}
}

// doGet issues a GET with the given headers and returns the response, whose
// body the caller must close.
func doGet(client *http.Client, url string, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return client.Do(req)
}
