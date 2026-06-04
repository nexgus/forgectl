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

// githubPlatform 實作 platform 介面, 對應 GitHub REST API. Asset 即 release asset:
// asset upload 將檔案暫存於 draft release, release create 正式發布,
// 下載則透過 asset API endpoint (CLAUDE.md).
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

// ghAsset / ghRelease 對應 GitHub REST 回應, 只保留各指令所需的欄位.
// assets 陣列僅含使用者上傳的 asset (自動產生的原始碼壓縮檔另以 zipball / tarball URL 存取).
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

// normalizeAssets 將 release asset 轉為統一格式, 並以 asset API endpoint 作為下載 handle.
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

// tagCommit 解析 release 的 tag 所指向的 commit, 並會解參考 annotated tag.
// 只逐一解析有 release 的 tag (CLAUDE.md). 無法解析的 tag (例如 draft 的 tag 尚未建立) 回傳 "".
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
		return fmt.Errorf("release %s 已存在 (已發布); 不覆寫", version)
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
		// asset upload 暫存的 draft: 將其轉為正式 release,
		// 寫入 note (若 tag 尚不存在, 則依 target_commitish 建立).
		payload := map[string]any{"draft": false, "name": version, "body": note, "tag_name": version}
		if commitish != "" {
			payload["target_commitish"] = commitish
		}
		return g.sendJSON("PATCH", g.reposURL("/releases/%d", existing.ID), payload, "發布 release "+version)
	}

	payload := map[string]any{"tag_name": version, "name": version, "body": note}
	if commitish != "" {
		payload["target_commitish"] = commitish
	}
	return g.sendJSON("POST", g.reposURL("/releases"), payload, "建立 release "+version)
}

// targetCommitish 決定新 tag 的 target_commitish. 若 tag 已存在則無需指定 (回傳 "");
// 否則必須提供 --commit, 值為 "latest" 時解析為預設分支的最新 commit.
func (g *githubPlatform) targetCommitish(tagExists bool, commit, version string) (string, error) {
	if tagExists {
		return "", nil
	}
	if commit == "" {
		return "", fmt.Errorf("tag %s 不存在; 請以 --commit 指定 commit SHA 或 'latest'", version)
	}
	if commit == "latest" {
		return g.defaultBranch()
	}
	return commit, nil
}

// findReleaseByTag 回傳 tag_name 等於 tag 的 release, 找不到則回傳 nil.
// 此函式掃描 release 清單而非使用 GET /releases/tags/{tag}, 因為該 endpoint 不回傳 draft,
// 而 asset upload 留下的正是 draft release.
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
				return 0, nil // 找到目標, 停止分頁
			}
		}
		return len(batch), nil
	})
	return found, err
}

// tagExists 回報 git tag 是否存在於 repository.
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
		return false, statusError("檢查 tag "+tag, status, body)
	}
}

// defaultBranch 回傳 repository 的預設分支名稱, 用於解析 --commit latest.
func (g *githubPlatform) defaultBranch() (string, error) {
	var r struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := g.getJSON(g.reposURL(""), &r); err != nil {
		return "", err
	}
	if r.DefaultBranch == "" {
		return "", fmt.Errorf("無法取得 %s/%s 的預設分支", g.owner, g.repo)
	}
	return r.DefaultBranch, nil
}

func (g *githubPlatform) newUploader(version string) (uploader, error) {
	rel, err := g.findReleaseByTag(version)
	if err != nil {
		return nil, err
	}
	if rel == nil {
		// 此 tag 尚無 release: 在新 draft 上暫存 asset
		// (tag 可暫不存在; release create 稍後正式發布 — CLAUDE.md).
		rel, err = g.createDraft(version)
		if err != nil {
			return nil, err
		}
	}
	return &githubUploader{g: g, releaseID: rel.ID, uploadURL: rel.UploadURL}, nil
}

// createDraft 建立一個 draft release 以暫存 asset.
func (g *githubPlatform) createDraft(version string) (*ghRelease, error) {
	data, _ := json.Marshal(map[string]any{"tag_name": version, "draft": true})
	status, body, err := g.req("POST", g.reposURL("/releases"), map[string]string{"Content-Type": "application/json"}, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if !ok2xx(status) {
		return nil, statusError("建立 draft release "+version, status, body)
	}
	var rel ghRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, fmt.Errorf("解析建立的 release: %w", err)
	}
	return &rel, nil
}

// githubUploader 將檔案暫存於一個 (取得或建立的) release,
// 跨檔案重複使用同一個 release id 及其 upload_url template.
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
	// GitHub 不允許重複的 asset 名稱, 故先刪除同名 asset 再上傳;
	// 語意為「此即目前的 asset」(docs/cli.md).
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
		return statusError("上傳 "+file.name, status, body)
	}
	return nil
}

// deleteAssetByName 刪除 release 中所有與指定名稱相符的 asset.
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
			return statusError("刪除既有 asset "+name, status, body)
		}
	}
	return nil
}

// uploadEndpoint 將 GitHub 的 upload_url template (".../assets{?name,label}")
// 填入 asset 名稱. 使用 template 可確保 GitHub Enterprise 的 upload host 正確,
// 無需寫死 uploads.github.com.
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
		return nil, fmt.Errorf("release %q 不存在", version)
	}
	if !ok2xx(status) {
		return nil, statusError("GET "+endpoint, status, body)
	}
	var rel ghRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, fmt.Errorf("解析 release: %w", err)
	}
	return g.normalizeAssets(rel.Assets), nil
}

func (g *githubPlatform) download(a asset, w io.Writer) error {
	// asset API endpoint 會附上 token 串流位元組, 因此私有 repo 可正常下載;
	// 若改用 browser_download_url 則無法攜帶 token (CLAUDE.md).
	return g.getStream(a.ref, map[string]string{"Accept": "application/octet-stream"}, w)
}
