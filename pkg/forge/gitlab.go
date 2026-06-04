package forge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
)

const gitlabPerPage = 100

// gitlabPlatform implements platform against the GitLab REST API. Assets are
// generic package files plus, once a release exists, asset links that point at
// the stable by-name download URL (CLAUDE.md). The project id is the
// URL-encoded "namespace/project" path, so no lookup by name is needed.
type gitlabPlatform struct {
	apiCaller
	base    string // already includes /api/v4
	project string // URL-encoded project path, used as the :id path segment
	pkgName string // generic package name = the project's final path segment
}

func newGitLabPlatform(client *http.Client, base, token, repo string) (*gitlabPlatform, error) {
	repo = strings.Trim(repo, "/")
	parts := strings.Split(repo, "/")
	if len(parts) < 2 {
		return nil, fmt.Errorf("repo must be a \"namespace/project\" path, got %q", repo)
	}
	for _, p := range parts {
		if p == "" {
			return nil, fmt.Errorf("repo must be a \"namespace/project\" path, got %q", repo)
		}
	}
	headers := map[string]string{}
	if token != "" {
		headers["PRIVATE-TOKEN"] = token
	}
	return &gitlabPlatform{
		apiCaller: apiCaller{http: client, authHeaders: headers},
		base:      base,
		project:   url.PathEscape(repo),
		pkgName:   path.Base(repo),
	}, nil
}

// glRelease / glLink mirror the GitLab REST responses, with only the fields the
// commands use. Source archives live in assets.sources (excluded); only
// assets.links are user assets.
type glRelease struct {
	Name     string `json:"name"`
	TagName  string `json:"tag_name"`
	Upcoming bool   `json:"upcoming_release"`
	Assets   struct {
		Links []glLink `json:"links"`
	} `json:"assets"`
}

type glLink struct {
	Name           string `json:"name"`
	URL            string `json:"url"`
	DirectAssetURL string `json:"direct_asset_url"`
}

func (g *gitlabPlatform) projectURL(format string, args ...any) string {
	return fmt.Sprintf("%s/projects/%s", g.base, g.project) + fmt.Sprintf(format, args...)
}

func (g *gitlabPlatform) listReleases() ([]release, error) {
	var raw []glRelease
	err := paginate(gitlabPerPage, func(page int) (int, error) {
		var batch []glRelease
		if err := g.getJSON(g.projectURL("/releases?per_page=%d&page=%d", gitlabPerPage, page), &batch); err != nil {
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
		up := r.Upcoming
		out = append(out, release{
			Name:     r.Name,
			Tag:      r.TagName,
			Upcoming: &up,
			Commit:   g.tagCommit(r.TagName),
			Assets:   g.linkAssets(r.Assets.Links),
		})
	}
	return out, nil
}

// linkAssets converts a release's asset links to the normalized form. GitLab
// reports no size for links, so Size stays nil (docs/cli.md).
func (g *gitlabPlatform) linkAssets(links []glLink) []asset {
	out := make([]asset, 0, len(links))
	for _, l := range links {
		ref := l.URL
		if ref == "" {
			ref = l.DirectAssetURL
		}
		out = append(out, asset{Name: l.Name, URL: l.URL, ref: ref})
	}
	return out
}

// tagCommit resolves a tag to its commit SHA. GitLab embeds a commit in the
// release object, but CLAUDE.md requires resolving the tag so both platforms
// behave identically. An unresolvable tag yields "".
func (g *gitlabPlatform) tagCommit(tag string) string {
	if tag == "" {
		return ""
	}
	status, body, err := g.req("GET", g.projectURL("/repository/tags/%s", url.PathEscape(tag)), nil, nil)
	if err != nil || !ok2xx(status) {
		return ""
	}
	var t struct {
		Commit struct {
			ID string `json:"id"`
		} `json:"commit"`
	}
	if json.Unmarshal(body, &t) != nil {
		return ""
	}
	return t.Commit.ID
}

func (g *gitlabPlatform) createRelease(version, note, commit string) error {
	exists, err := g.releaseExists(version)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("release %s already exists; not overwriting", version)
	}

	tagExists, err := g.tagExists(version)
	if err != nil {
		return err
	}
	payload := map[string]any{"tag_name": version, "name": version, "description": note}
	if !tagExists {
		ref, err := g.ref(commit, version)
		if err != nil {
			return err
		}
		payload["ref"] = ref
	}
	if err := g.sendJSON("POST", g.projectURL("/releases"), payload, "creating release "+version); err != nil {
		return err
	}
	// Backfill asset links for the version's already-uploaded package files
	// (CLAUDE.md).
	return g.linkPackageFiles(version)
}

// ref decides the ref a new tag points to: a SHA, or the default branch for
// "latest". --commit is required when the tag does not yet exist.
func (g *gitlabPlatform) ref(commit, version string) (string, error) {
	if commit == "" {
		return "", fmt.Errorf("tag %s does not exist; specify --commit (a commit SHA or 'latest')", version)
	}
	if commit == "latest" {
		return g.defaultBranch()
	}
	return commit, nil
}

// releaseExists reports whether a Release exists for the tag.
func (g *gitlabPlatform) releaseExists(version string) (bool, error) {
	return g.exists(g.projectURL("/releases/%s", url.PathEscape(version)), "checking release "+version)
}

// tagExists reports whether the git tag exists in the repository.
func (g *gitlabPlatform) tagExists(tag string) (bool, error) {
	return g.exists(g.projectURL("/repository/tags/%s", url.PathEscape(tag)), "checking tag "+tag)
}

// exists performs a GET that distinguishes 200 (exists) from 404 (absent).
func (g *gitlabPlatform) exists(url, action string) (bool, error) {
	status, body, err := g.req("GET", url, nil, nil)
	if err != nil {
		return false, err
	}
	switch status {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, statusError(action, status, body)
	}
}

// defaultBranch returns the project's default branch, used to resolve --commit
// latest.
func (g *gitlabPlatform) defaultBranch() (string, error) {
	var p struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := g.getJSON(g.projectURL(""), &p); err != nil {
		return "", err
	}
	if p.DefaultBranch == "" {
		return "", fmt.Errorf("could not determine the default branch")
	}
	return p.DefaultBranch, nil
}

// glPackage / glPackageFile mirror the package registry responses.
type glPackage struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Type    string `json:"package_type"`
}

type glPackageFile struct {
	ID       int64  `json:"id"`
	FileName string `json:"file_name"`
}

// findPackage returns the id of the generic package for this project and
// version, or 0 if none exists yet.
func (g *gitlabPlatform) findPackage(version string) (int64, error) {
	var id int64
	err := paginate(gitlabPerPage, func(page int) (int, error) {
		var batch []glPackage
		endpoint := g.projectURL("/packages?package_type=generic&per_page=%d&page=%d", gitlabPerPage, page)
		if err := g.getJSON(endpoint, &batch); err != nil {
			return 0, err
		}
		for _, p := range batch {
			if p.Name == g.pkgName && p.Version == version {
				id = p.ID
				return 0, nil // stop paging: target found
			}
		}
		return len(batch), nil
	})
	return id, err
}

// packageFiles lists the files of a package.
func (g *gitlabPlatform) packageFiles(pkgID int64) ([]glPackageFile, error) {
	var files []glPackageFile
	if err := g.getJSON(g.projectURL("/packages/%d/package_files", pkgID), &files); err != nil {
		return nil, err
	}
	return files, nil
}

// linkPackageFiles links every generic package file of version to the release
// as an asset link pointing at the stable by-name download URL. delete-then-
// upload keeps one file per name, so the link set is unambiguous (CLAUDE.md).
func (g *gitlabPlatform) linkPackageFiles(version string) error {
	pkgID, err := g.findPackage(version)
	if err != nil {
		return err
	}
	if pkgID == 0 {
		return nil // no uploaded assets for this version
	}
	files, err := g.packageFiles(pkgID)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, f := range files {
		if seen[f.FileName] {
			continue
		}
		seen[f.FileName] = true
		if err := g.createLink(version, f.FileName); err != nil {
			return err
		}
	}
	return nil
}

// createLink adds an asset link to the release, ignoring a name that already
// exists so that upload-time and create-time linking converge on one set.
func (g *gitlabPlatform) createLink(version, name string) error {
	payload := map[string]any{"name": name, "url": g.byNameURL(version, name)}
	data, _ := json.Marshal(payload)
	endpoint := g.projectURL("/releases/%s/assets/links", url.PathEscape(version))
	status, body, err := g.req("POST", endpoint, map[string]string{"Content-Type": "application/json"}, bytes.NewReader(data))
	if err != nil {
		return err
	}
	if ok2xx(status) {
		return nil
	}
	// A link with this name is already present: leave it as-is.
	if status == http.StatusBadRequest && strings.Contains(strings.ToLower(string(body)), "already") {
		return nil
	}
	return statusError("linking asset "+name, status, body)
}

// byNameURL is the stable by-name generic-package download URL for a file. It
// is the link target and the upload destination, so they always agree.
func (g *gitlabPlatform) byNameURL(version, name string) string {
	return g.projectURL("/packages/generic/%s/%s/%s",
		url.PathEscape(g.pkgName), url.PathEscape(version), url.PathEscape(name))
}

func (g *gitlabPlatform) newUploader(version string) (uploader, error) {
	hasRelease, err := g.releaseExists(version)
	if err != nil {
		return nil, err
	}
	return &gitlabUploader{g: g, version: version, hasRelease: hasRelease}, nil
}

// gitlabUploader uploads files for one version, remembering whether the release
// already exists so it can attach links as it goes.
type gitlabUploader struct {
	g          *gitlabPlatform
	version    string
	hasRelease bool
}

func (u *gitlabUploader) upload(file localAsset) error {
	data, err := readLocalFile(file.path)
	if err != nil {
		return err
	}
	// Deterministic overwrite: delete any same-name package file first, then
	// upload, so the registry never holds duplicates (CLAUDE.md).
	if err := u.g.deletePackageFile(u.version, file.name); err != nil {
		return err
	}
	headers := map[string]string{"Content-Type": contentType(file.name)}
	status, body, err := u.g.req("PUT", u.g.byNameURL(u.version, file.name), headers, bytes.NewReader(data))
	if err != nil {
		return err
	}
	if !ok2xx(status) {
		return statusError("uploading "+file.name, status, body)
	}
	// If the release already exists, attach a by-name link now; otherwise
	// release create backfills it later.
	if u.hasRelease {
		return u.g.createLink(u.version, file.name)
	}
	return nil
}

// deletePackageFile removes every package file named name in the version's
// generic package (none on the first upload). Deleting requires higher
// privilege; a 403 surfaces as an error (CLAUDE.md).
func (g *gitlabPlatform) deletePackageFile(version, name string) error {
	pkgID, err := g.findPackage(version)
	if err != nil {
		return err
	}
	if pkgID == 0 {
		return nil
	}
	files, err := g.packageFiles(pkgID)
	if err != nil {
		return err
	}
	for _, f := range files {
		if f.FileName != name {
			continue
		}
		status, body, err := g.req("DELETE", g.projectURL("/packages/%d/package_files/%d", pkgID, f.ID), nil, nil)
		if err != nil {
			return err
		}
		if !ok2xx(status) && status != http.StatusNotFound {
			return statusError("deleting existing package file "+name, status, body)
		}
	}
	return nil
}

func (g *gitlabPlatform) findReleaseAssets(version string) ([]asset, error) {
	var rel glRelease
	if version == "latest" {
		latest, err := g.latestRelease()
		if err != nil {
			return nil, err
		}
		rel = *latest
	} else {
		status, body, err := g.req("GET", g.projectURL("/releases/%s", url.PathEscape(version)), nil, nil)
		if err != nil {
			return nil, err
		}
		if status == http.StatusNotFound {
			return nil, fmt.Errorf("release %q not found", version)
		}
		if !ok2xx(status) {
			return nil, statusError("GET release "+version, status, body)
		}
		if err := json.Unmarshal(body, &rel); err != nil {
			return nil, fmt.Errorf("parsing release: %w", err)
		}
	}
	return g.linkAssets(rel.Assets.Links), nil
}

// latestRelease returns the newest non-upcoming release. GitLab lists releases
// newest-first; "latest" excludes upcoming ones (the GitLab analogue of
// excluding draft / prerelease).
func (g *gitlabPlatform) latestRelease() (*glRelease, error) {
	var batch []glRelease
	if err := g.getJSON(g.projectURL("/releases?per_page=%d&page=1", gitlabPerPage), &batch); err != nil {
		return nil, err
	}
	for i := range batch {
		if !batch[i].Upcoming {
			return &batch[i], nil
		}
	}
	return nil, fmt.Errorf("no published release found")
}

func (g *gitlabPlatform) download(a asset, w io.Writer) error {
	// The link target is the by-name generic-package download URL; the token in
	// the auth headers authorizes private downloads (CLAUDE.md).
	return g.getStream(a.ref, nil, w)
}
