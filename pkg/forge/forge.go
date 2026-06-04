// Package forge performs the actual GitHub / GitLab REST work behind each
// command. It receives everything it needs as parameters — connection and auth
// settings through New, per-command inputs through each method — so it depends
// on no CLI types and can be tested in isolation.
//
// The shared connection and credential resolution live in endpoint.go and
// credential.go; the platform-agnostic command orchestration in releases.go and
// assets.go; and the per-source REST work behind the platform interface in
// github.go and gitlab.go. ping.go is the first command and exercises the same
// connection and credential resolution.
package forge

import (
	"fmt"
	"io"
	"os"
	"strings"
)

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

// platform abstracts the per-source (GitHub / GitLab) REST operations behind
// the release and asset commands. Client builds the right implementation from
// its Config; the command methods orchestrate platform calls and own the
// cross-cutting concerns (output formatting, glob matching, local file I/O, and
// the per-file success / failure tally on upload).
type platform interface {
	// listReleases returns every release, each with its commit resolved from
	// the release's own tag (only release tags are resolved, one at a time).
	listReleases() ([]release, error)

	// createRelease publishes a release for version with the given note. commit
	// names the commit a new tag points to (a SHA, or "latest" for the default
	// branch's head); it is required only when the tag does not yet exist.
	createRelease(version, note, commit string) error

	// newUploader prepares the upload target for version — a get-or-created
	// draft release on GitHub, a resolved project and package on GitLab — so
	// several files reuse one preparation.
	newUploader(version string) (uploader, error)

	// findReleaseAssets resolves version ("latest" is allowed) to its assets,
	// or returns an error when no such release exists.
	findReleaseAssets(version string) ([]asset, error)

	// download streams the asset's bytes to w.
	download(a asset, w io.Writer) error
}

// uploader is a prepared upload target so several files reuse one preparation
// (one get-or-create release on GitHub, one project / package resolution on
// GitLab).
type uploader interface {
	// upload writes one local file as an asset, overwriting any same-name asset.
	upload(file localAsset) error
}

// platform resolves the connection and credentials for repo, emits the shared
// warnings, and builds the source-specific platform implementation.
func (c *Client) platform(repo string) (platform, error) {
	base, err := apiBase(c.cfg.Source, c.cfg.Host)
	if err != nil {
		return nil, err
	}
	host, err := credentialHost(c.cfg.Source, c.cfg.Host)
	if err != nil {
		return nil, err
	}
	a, err := resolveAuth(c.cfg, host)
	if err != nil {
		return nil, err
	}
	emitWarnings(a, c.cfg.Insecure)

	client := newHTTPClient(c.cfg.Insecure)
	switch c.cfg.Source {
	case "github":
		owner, name, err := splitRepo(repo)
		if err != nil {
			return nil, err
		}
		return newGitHubPlatform(client, base, a.Token, owner, name), nil
	case "gitlab":
		return newGitLabPlatform(client, base, a.Token, repo)
	default:
		return nil, fmt.Errorf("unknown source %q", c.cfg.Source)
	}
}

// emitWarnings prints the shared stderr warnings a command shows before doing
// work: any credential-file permission warning (collected during credential
// resolution) and the --insecure notice. Ping prints the same set inline.
func emitWarnings(a auth, insecure bool) {
	for _, w := range a.Warnings {
		fmt.Fprintln(os.Stderr, "warning: "+w)
	}
	if insecure {
		fmt.Fprintln(os.Stderr, insecureWarning)
	}
}

// insecureWarning is the stderr notice shown whenever TLS verification is off.
const insecureWarning = "warning: TLS certificate verification is disabled (--insecure); use only on trusted hosts"

// splitRepo splits a GitHub "owner/repo" path into its two parts. GitLab paths
// may contain subgroups and are handled by the GitLab platform instead.
func splitRepo(repo string) (owner, name string, err error) {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("repo must be an \"owner/repo\" path, got %q", repo)
	}
	return parts[0], parts[1], nil
}
