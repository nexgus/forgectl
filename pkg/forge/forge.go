// Package forge performs the actual GitHub / GitLab REST work behind each
// command. It receives everything it needs as parameters — connection and auth
// settings through New, per-command inputs through each method — so it depends
// on no CLI types and can be tested in isolation.
//
// Ping is implemented (see ping.go), along with the shared connection and
// credential resolution it needs (endpoint.go, credential.go). The release and
// asset handlers are still the wiring skeleton and report notImplemented.
package forge

import "fmt"

// Config carries the resolved global settings a Client needs. cmd/forgectl
// builds it from the parsed global flags, which keeps forge free of any CLI
// type (and parallel-testable, since it reads no shared global).
type Config struct {
	Source    string // "github" or "gitlab"
	Host      string // base URL of a self-hosted instance; empty for the public site
	Insecure  bool   // skip TLS certificate verification
	Token     string // token override
	TokenFile string // path to read the token from
	User      string // user override
}

// Client talks to one hosting platform, configured by a Config.
type Client struct {
	cfg Config
}

// New returns a Client for the given configuration.
func New(cfg Config) *Client {
	return &Client{cfg: cfg}
}

// notImplemented reports that a handler is wired but its behavior is not written
// yet. It names the platform from the Config so the wiring is visible.
func (c *Client) notImplemented(cmd string) error {
	return fmt.Errorf("%s: not implemented yet (source=%q)", cmd, c.cfg.Source)
}

// ReleaseList implements: forgectl release list <repo> [--json]
func (c *Client) ReleaseList(repo string, asJSON bool) error {
	return c.notImplemented("release list")
}

// ReleaseCreate implements:
// forgectl release create <repo> <version> (--note STR | --note-file PATH) [--commit COMMIT]
func (c *Client) ReleaseCreate(repo, version, note, noteFile, commit string) error {
	return c.notImplemented("release create")
}

// AssetUpload implements: forgectl asset upload <repo> <version> <path>[=NAME]...
func (c *Client) AssetUpload(repo, version string, paths []string) error {
	return c.notImplemented("asset upload")
}

// AssetDownload implements:
// forgectl asset download <repo> <version> [pattern]... [-d DIR] [-o NAME] [--overwrite]
func (c *Client) AssetDownload(repo, version string, patterns []string, dir, output string, overwrite bool) error {
	return c.notImplemented("asset download")
}
