package forge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	githubAccept     = "application/vnd.github+json"
	githubAPIVersion = "2022-11-28"
	githubPerPage    = 100
)

// githubPlatform implements platform against the GitHub REST API. Assets are
// release assets: asset upload stages them on a draft release, release create
// publishes it, and downloads go through the asset API endpoint (CLAUDE.md).
type githubPlatform struct {
	apiCaller
	base  string
	owner string
	repo  string
}

func newGitHubPlatform(client *http.Client, base, token, owner, repo string) *githubPlatform {
	headers := map[string]string{
		"Accept":               githubAccept,
		"X-GitHub-Api-Version": githubAPIVersion,
	}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	return &githubPlatform{
		apiCaller: apiCaller{http: client, authHeaders: headers},
		base:      base,
		owner:     owner,
		repo:      repo,
	}
}

// ghAsset / ghRelease mirror the GitHub REST responses, with only the fields
// the commands use. The assets array holds user-uploaded assets only (the
// auto-generated source archives are separate zipball / tarball URLs).
type ghAsset struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type ghRelease struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	TagName    string    `json:"tag_name"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	UploadURL  string    `json:"upload_url"`
	Assets     []ghAsset `json:"assets"`
}

func (g *githubPlatform) reposURL(format string, args ...any) string {
	return fmt.Sprintf("%s/repos/%s/%s", g.base, g.owner, g.repo) + fmt.Sprintf(format, args...)
}

func (g *githubPlatform) listReleases() ([]release, error) {
	var raw []ghRelease
	err := paginate(githubPerPage, func(page int) (int, error) {
		var batch []ghRelease
		if err := g.getJSON(g.reposURL("/releases?per_page=%d&page=%d", githubPerPage, page), &batch); err != nil {
			return 0, err
		}
		raw = append(raw, batch...)
		return len(batch), nil
	})
	if err != nil {
		return nil, err
	}

	out := make([]release, 0, len(raw))
	for _, r := range raw {
		draft, pre := r.Draft, r.Prerelease
		rel := release{
			Name:       r.Name,
			Tag:        r.TagName,
			Draft:      &draft,
			Prerelease: &pre,
			Commit:     g.tagCommit(r.TagName),
			Assets:     g.normalizeAssets(r.Assets),
		}
		out = append(out, rel)
	}
	return out, nil
}

// normalizeAssets converts release assets to the normalized form, recording the
// asset API endpoint as the download handle.
func (g *githubPlatform) normalizeAssets(assets []ghAsset) []asset {
	out := make([]asset, 0, len(assets))
	for _, a := range assets {
		size := a.Size
		out = append(out, asset{
			Name: a.Name,
			URL:  a.BrowserDownloadURL,
			Size: &size,
			ref:  g.reposURL("/releases/assets/%d", a.ID),
		})
	}
	return out
}

// tagCommit resolves the commit a release's tag points to, dereferencing an
// annotated tag. Only release tags are resolved, one at a time (CLAUDE.md). An
// unresolvable tag (e.g. a draft whose tag does not exist yet) yields "".
func (g *githubPlatform) tagCommit(tag string) string {
	if tag == "" {
		return ""
	}
	status, body, err := g.req("GET", g.reposURL("/commits/%s", url.PathEscape(tag)), nil, nil)
	if err != nil || !ok2xx(status) {
		return ""
	}
	var c struct {
		SHA string `json:"sha"`
	}
	if json.Unmarshal(body, &c) != nil {
		return ""
	}
	return c.SHA
}

func (g *githubPlatform) createRelease(version, note, commit string) error {
	existing, err := g.findReleaseByTag(version)
	if err != nil {
		return err
	}
	if existing != nil && !existing.Draft {
		return fmt.Errorf("release %s already exists (published); not overwriting", version)
	}

	tagExists, err := g.tagExists(version)
	if err != nil {
		return err
	}
	commitish, err := g.targetCommitish(tagExists, commit, version)
	if err != nil {
		return err
	}

	if existing != nil {
		// A draft staged by asset upload: turn it into a published release,
		// writing the note (and creating the tag from target_commitish if it
		// does not exist yet).
		payload := map[string]any{"draft": false, "name": version, "body": note, "tag_name": version}
		if commitish != "" {
			payload["target_commitish"] = commitish
		}
		return g.sendJSON("PATCH", g.reposURL("/releases/%d", existing.ID), payload, "publishing release "+version)
	}

	payload := map[string]any{"tag_name": version, "name": version, "body": note}
	if commitish != "" {
		payload["target_commitish"] = commitish
	}
	return g.sendJSON("POST", g.reposURL("/releases"), payload, "creating release "+version)
}

// targetCommitish decides the target_commitish for a new tag. When the tag
// already exists it is irrelevant (""); otherwise --commit is required and
// "latest" resolves to the default branch's head.
func (g *githubPlatform) targetCommitish(tagExists bool, commit, version string) (string, error) {
	if tagExists {
		return "", nil
	}
	if commit == "" {
		return "", fmt.Errorf("tag %s does not exist; specify --commit (a commit SHA or 'latest')", version)
	}
	if commit == "latest" {
		return g.defaultBranch()
	}
	return commit, nil
}

// findReleaseByTag returns the release whose tag_name equals tag, or nil. It
// scans the release list rather than GET /releases/tags/{tag} because that
// endpoint omits drafts, and a draft is exactly what asset upload leaves behind.
func (g *githubPlatform) findReleaseByTag(tag string) (*ghRelease, error) {
	var found *ghRelease
	err := paginate(githubPerPage, func(page int) (int, error) {
		var batch []ghRelease
		if err := g.getJSON(g.reposURL("/releases?per_page=%d&page=%d", githubPerPage, page), &batch); err != nil {
			return 0, err
		}
		for i := range batch {
			if batch[i].TagName == tag {
				r := batch[i]
				found = &r
				return 0, nil // stop paging: target found
			}
		}
		return len(batch), nil
	})
	return found, err
}

// tagExists reports whether the git tag exists in the repository.
func (g *githubPlatform) tagExists(tag string) (bool, error) {
	status, body, err := g.req("GET", g.reposURL("/git/ref/tags/%s", url.PathEscape(tag)), nil, nil)
	if err != nil {
		return false, err
	}
	switch status {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, statusError("checking tag "+tag, status, body)
	}
}

// defaultBranch returns the repository's default branch name, used to resolve
// --commit latest.
func (g *githubPlatform) defaultBranch() (string, error) {
	var r struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := g.getJSON(g.reposURL(""), &r); err != nil {
		return "", err
	}
	if r.DefaultBranch == "" {
		return "", fmt.Errorf("could not determine the default branch of %s/%s", g.owner, g.repo)
	}
	return r.DefaultBranch, nil
}

func (g *githubPlatform) newUploader(version string) (uploader, error) {
	rel, err := g.findReleaseByTag(version)
	if err != nil {
		return nil, err
	}
	if rel == nil {
		// No release for this tag yet: stage assets on a fresh draft (the tag
		// need not exist; release create publishes it later — CLAUDE.md).
		rel, err = g.createDraft(version)
		if err != nil {
			return nil, err
		}
	}
	return &githubUploader{g: g, releaseID: rel.ID, uploadURL: rel.UploadURL}, nil
}

// createDraft creates a draft release to stage assets on.
func (g *githubPlatform) createDraft(version string) (*ghRelease, error) {
	data, _ := json.Marshal(map[string]any{"tag_name": version, "draft": true})
	status, body, err := g.req("POST", g.reposURL("/releases"), map[string]string{"Content-Type": "application/json"}, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if !ok2xx(status) {
		return nil, statusError("creating draft release "+version, status, body)
	}
	var rel ghRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, fmt.Errorf("parsing created release: %w", err)
	}
	return &rel, nil
}

// githubUploader stages files on one (get-or-created) release, reusing the
// release id and its upload_url template across files.
type githubUploader struct {
	g         *githubPlatform
	releaseID int64
	uploadURL string
}

func (u *githubUploader) upload(file localAsset) error {
	data, err := readLocalFile(file.path)
	if err != nil {
		return err
	}
	// GitHub rejects a duplicate asset name, so remove any same-name asset
	// first; upload is "this is now the asset" (docs/cli.md).
	if err := u.g.deleteAssetByName(u.releaseID, file.name); err != nil {
		return err
	}
	endpoint := uploadEndpoint(u.uploadURL, file.name)
	headers := map[string]string{"Content-Type": contentType(file.name)}
	status, body, err := u.g.req("POST", endpoint, headers, bytes.NewReader(data))
	if err != nil {
		return err
	}
	if !ok2xx(status) {
		return statusError("uploading "+file.name, status, body)
	}
	return nil
}

// deleteAssetByName removes any asset of the release that has the given name.
func (g *githubPlatform) deleteAssetByName(releaseID int64, name string) error {
	var assets []ghAsset
	if err := g.getJSON(g.reposURL("/releases/%d/assets?per_page=100", releaseID), &assets); err != nil {
		return err
	}
	for _, a := range assets {
		if a.Name != name {
			continue
		}
		status, body, err := g.req("DELETE", g.reposURL("/releases/assets/%d", a.ID), nil, nil)
		if err != nil {
			return err
		}
		if !ok2xx(status) && status != http.StatusNotFound {
			return statusError("deleting existing asset "+name, status, body)
		}
	}
	return nil
}

// uploadEndpoint fills GitHub's upload_url template (".../assets{?name,label}")
// with the asset name. Using the template keeps GitHub Enterprise's upload host
// correct without hardcoding uploads.github.com.
func uploadEndpoint(template, name string) string {
	base := template
	if i := strings.IndexByte(base, '{'); i >= 0 {
		base = base[:i]
	}
	return base + "?name=" + url.QueryEscape(name)
}

func (g *githubPlatform) findReleaseAssets(version string) ([]asset, error) {
	var endpoint string
	if version == "latest" {
		endpoint = g.reposURL("/releases/latest")
	} else {
		endpoint = g.reposURL("/releases/tags/%s", url.PathEscape(version))
	}
	status, body, err := g.req("GET", endpoint, nil, nil)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, fmt.Errorf("release %q not found", version)
	}
	if !ok2xx(status) {
		return nil, statusError("GET "+endpoint, status, body)
	}
	var rel ghRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, fmt.Errorf("parsing release: %w", err)
	}
	return g.normalizeAssets(rel.Assets), nil
}

func (g *githubPlatform) download(a asset, w io.Writer) error {
	// The asset API endpoint streams the bytes with the token attached, so
	// private repos work; browser_download_url would not (CLAUDE.md).
	return g.getStream(a.ref, map[string]string{"Accept": "application/octet-stream"}, w)
}
