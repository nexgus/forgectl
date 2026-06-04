package forge

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// release is the normalized, cross-platform view of one release. Fields a
// platform has no concept of are nil pointers (GitLab has no draft / prerelease;
// GitHub has no upcoming), which lets release list render and the --json output
// emit null exactly where docs/cli.md says to.
type release struct {
	Name       string
	Tag        string
	Draft      *bool // GitHub only
	Prerelease *bool // GitHub only
	Upcoming   *bool // GitLab only
	Commit     string
	Assets     []asset
}

// asset is the normalized view of one downloadable asset. Size is nil where the
// platform does not report one (GitLab asset links carry no size). ref is the
// platform-specific download handle (a GitHub asset API URL, a GitLab link URL)
// and is not part of any output.
type asset struct {
	Name string
	URL  string
	Size *int64
	ref  string
}

// ReleaseList implements: forgectl release list <repo> [--json]
func (c *Client) ReleaseList(repo string, asJSON bool) error {
	p, err := c.platform(repo)
	if err != nil {
		return err
	}
	releases, err := p.listReleases()
	if err != nil {
		return err
	}
	if asJSON {
		return printReleasesJSON(os.Stdout, releases)
	}
	printReleasesText(os.Stdout, releases)
	return nil
}

// ReleaseCreate implements:
// forgectl release create <repo> <version> (--note STR | --note-file PATH) [--commit COMMIT]
func (c *Client) ReleaseCreate(repo, version, note, noteFile, commit string) error {
	text, err := noteText(note, noteFile)
	if err != nil {
		return err
	}
	p, err := c.platform(repo)
	if err != nil {
		return err
	}
	if err := p.createRelease(version, text, commit); err != nil {
		return err
	}
	fmt.Printf("release %s created\n", version)
	return nil
}

// noteText resolves the release note from the inline --note or the --note-file
// path; the CLI enforces that exactly one is given. --note-file reads the whole
// file as the note (like --token-file, no stdin "-").
func noteText(note, noteFile string) (string, error) {
	if noteFile != "" {
		data, err := os.ReadFile(noteFile)
		if err != nil {
			return "", fmt.Errorf("reading --note-file %s: %w", noteFile, err)
		}
		return string(data), nil
	}
	return note, nil
}

// printReleasesText renders the human-readable release listing of docs/cli.md.
// It shows no total count and separates entries with a blank line.
func printReleasesText(w io.Writer, releases []release) {
	if len(releases) == 0 {
		fmt.Fprintln(w, "沒有 release")
		return
	}
	for i, r := range releases {
		if i > 0 {
			fmt.Fprintln(w)
		}
		name := r.Name
		if name == "" {
			name = "(未命名)"
		}
		fmt.Fprintf(w, "[%d] %s\n", i+1, name)
		fmt.Fprintf(w, "    release tag: %s%s\n", r.Tag, releaseLabels(r))
		if r.Commit != "" {
			fmt.Fprintf(w, "    commit hash: %s\n", shortCommit(r.Commit))
		} else {
			fmt.Fprintln(w, "    commit hash: (未知)")
		}
		if len(r.Assets) == 0 {
			fmt.Fprintln(w, "    assets: 無")
			continue
		}
		fmt.Fprintf(w, "    assets (%d):\n", len(r.Assets))
		for _, a := range r.Assets {
			if a.Size != nil {
				fmt.Fprintf(w, "      - %s (%s)\n", a.Name, humanSize(*a.Size))
			} else {
				fmt.Fprintf(w, "      - %s\n", a.Name)
			}
			fmt.Fprintf(w, "        %s\n", a.URL)
		}
	}
}

// releaseLabels renders the status suffix on a release's tag line: GitHub's
// (draft) / (prerelease), GitLab's (upcoming). A plain published release has
// none.
func releaseLabels(r release) string {
	var s string
	if r.Draft != nil && *r.Draft {
		s += " (draft)"
	}
	if r.Prerelease != nil && *r.Prerelease {
		s += " (prerelease)"
	}
	if r.Upcoming != nil && *r.Upcoming {
		s += " (upcoming)"
	}
	return s
}

// releaseJSON / assetJSON are the --json shapes of docs/cli.md. The pointer
// fields marshal to null where a platform has no corresponding value.
type releaseJSON struct {
	Name       string      `json:"name"`
	Tag        string      `json:"tag"`
	Draft      *bool       `json:"draft"`
	Prerelease *bool       `json:"prerelease"`
	Upcoming   *bool       `json:"upcoming"`
	Commit     string      `json:"commit"`
	Assets     []assetJSON `json:"assets"`
}

type assetJSON struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Size *int64 `json:"size"`
}

// printReleasesJSON writes the releases as the JSON array of docs/cli.md.
func printReleasesJSON(w io.Writer, releases []release) error {
	out := make([]releaseJSON, 0, len(releases))
	for _, r := range releases {
		rj := releaseJSON{
			Name:       r.Name,
			Tag:        r.Tag,
			Draft:      r.Draft,
			Prerelease: r.Prerelease,
			Upcoming:   r.Upcoming,
			Commit:     r.Commit,
			Assets:     make([]assetJSON, 0, len(r.Assets)),
		}
		for _, a := range r.Assets {
			rj.Assets = append(rj.Assets, assetJSON{Name: a.Name, URL: a.URL, Size: a.Size})
		}
		out = append(out, rj)
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(w, string(data))
	return nil
}

// shortCommit abbreviates a commit SHA to its first seven characters for the
// text listing.
func shortCommit(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// humanSize renders a byte count as a human-readable size (B, KiB, MiB, ...),
// matching the release-list output in docs/cli.md.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
