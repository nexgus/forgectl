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

// gitlabPlatform 實作 platform 介面, 透過 GitLab REST API 操作. asset 是
// generic package file; 一旦 release 存在, 便建立 asset link 指向穩定的 by-name
// 下載 URL (見 CLAUDE.md). project id 為 URL-encoded 的 "namespace/project" 路徑,
// 無需依名稱查詢.
type gitlabPlatform struct {
	apiCaller
	base    string // 已包含 /api/v4
	project string // URL-encoded 的 project 路徑, 作為 :id 路徑片段
	pkgName string // generic package name = project 路徑的最後一段
}

func newGitLabPlatform(client *http.Client, base, token, repo string) (*gitlabPlatform, error) {
	repo = strings.Trim(repo, "/")
	parts := strings.Split(repo, "/")
	if len(parts) < 2 {
		return nil, fmt.Errorf("repo 必須為 \"namespace/project\" 格式, 實際為 %q", repo)
	}
	for _, p := range parts {
		if p == "" {
			return nil, fmt.Errorf("repo 必須為 \"namespace/project\" 格式, 實際為 %q", repo)
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

// glRelease / glLink 對應 GitLab REST 回應, 僅保留指令所需的欄位.
// 原始碼壓縮檔位於 assets.sources (已排除); 使用者 asset 僅取 assets.links.
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
	LinkType       string `json:"link_type"`
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
			Assets:   g.linkAssets(r.TagName, r.Assets.Links),
		})
	}
	return out, nil
}

// linkAssets 將 release 的 asset link 轉換為正規化格式. GitLab 不回報 link 的大小,
// 因此 Size 維持 nil (見 docs/cli.md). version 為該 release 的 tag, 用來重建下載端點.
//
// 下載端點 (ref): 對 link_type=package 的 asset, **不信任 link 存的 url**. GitLab 會把
// package 類型的 link url / direct_asset_url 正規化成 web permalink
// (/<repo>/-/package_files/:id/download), 該 web 路由只認瀏覽器 session, 帶 PRIVATE-TOKEN
// (header 或 query) 一律被 302 導向登入頁; 故改用 (pkgName, version, name) 重建 by-name
// generic package API URL — 它接受 PRIVATE-TOKEN, 與上傳目的地同一個 URL (見 CLAUDE.md).
// 非 package 類型 (外部 url) 則沿用 link 的 url.
func (g *gitlabPlatform) linkAssets(version string, links []glLink) []asset {
	out := make([]asset, 0, len(links))
	for _, l := range links {
		var ref string
		switch {
		case l.LinkType == "package" && version != "":
			ref = g.byNameURL(version, l.Name)
		case l.URL != "":
			ref = l.URL
		default:
			ref = l.DirectAssetURL
		}
		out = append(out, asset{Name: l.Name, URL: l.URL, ref: ref})
	}
	return out
}

// tagCommit 將 tag 解析為對應的 commit SHA. GitLab 雖在 release 物件中附帶 commit,
// 但 CLAUDE.md 要求透過解析 tag 取得, 以確保兩平台行為一致. 無法解析的 tag 回傳 "".
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
		return fmt.Errorf("release %s 已存在, 不覆寫", version)
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
	if err := g.sendJSON("POST", g.projectURL("/releases"), payload, "建立 release "+version); err != nil {
		return err
	}
	// 補建 asset link, 對應此 version 已上傳的 package file (見 CLAUDE.md).
	return g.linkPackageFiles(version)
}

// ref 決定新 tag 所指向的 ref: 可為 SHA, 或 "latest" 對應的預設分支.
// 當 tag 尚不存在時, 必須提供 --commit.
func (g *gitlabPlatform) ref(commit, version string) (string, error) {
	if commit == "" {
		return "", fmt.Errorf("tag %s 不存在, 請透過 --commit 指定 commit SHA 或 'latest'", version)
	}
	if commit == "latest" {
		return g.defaultBranch()
	}
	return commit, nil
}

// releaseExists 回報指定 tag 是否已有對應的 release.
func (g *gitlabPlatform) releaseExists(version string) (bool, error) {
	return g.exists(g.projectURL("/releases/%s", url.PathEscape(version)), "查詢 release "+version)
}

// tagExists 回報指定的 git tag 是否存在於 repository 中.
func (g *gitlabPlatform) tagExists(tag string) (bool, error) {
	return g.exists(g.projectURL("/repository/tags/%s", url.PathEscape(tag)), "查詢 tag "+tag)
}

// exists 發送 GET 請求, 區分 200 (存在) 與 404 (不存在) 兩種情況.
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

// defaultBranch 回傳專案的預設分支, 用於解析 --commit latest.
func (g *gitlabPlatform) defaultBranch() (string, error) {
	var p struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := g.getJSON(g.projectURL(""), &p); err != nil {
		return "", err
	}
	if p.DefaultBranch == "" {
		return "", fmt.Errorf("無法取得預設分支")
	}
	return p.DefaultBranch, nil
}

// glPackage / glPackageFile 對應 package registry 的回應結構.
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

// findPackage 回傳此專案與 version 對應的 generic package id,
// 若尚不存在則回傳 0.
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
				return 0, nil // 找到目標, 停止分頁
			}
		}
		return len(batch), nil
	})
	return id, err
}

// packageFiles 列出指定 package 的所有檔案.
func (g *gitlabPlatform) packageFiles(pkgID int64) ([]glPackageFile, error) {
	var files []glPackageFile
	if err := g.getJSON(g.projectURL("/packages/%d/package_files", pkgID), &files); err != nil {
		return nil, err
	}
	return files, nil
}

// linkPackageFiles 將指定 version 的每個 generic package file 連結至 release,
// 建立 asset link 指向穩定的 by-name 下載 URL. 先刪後傳確保每個檔名只有一份,
// 因此 link 集合不會有歧義 (見 CLAUDE.md).
func (g *gitlabPlatform) linkPackageFiles(version string) error {
	pkgID, err := g.findPackage(version)
	if err != nil {
		return err
	}
	if pkgID == 0 {
		return nil // 此 version 尚無已上傳的 asset
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

// createLink 在 release 中新增 asset link, 若同名 link 已存在則略過,
// 使上傳時與 release create 時的 link 最終匯聚為同一組.
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
	// 同名 link 已存在, 維持原狀不修改.
	if status == http.StatusBadRequest && strings.Contains(strings.ToLower(string(body)), "already") {
		return nil
	}
	return statusError("連結 asset "+name, status, body)
}

// byNameURL 回傳檔案的穩定 by-name generic package 下載 URL.
// 此 URL 同時作為 link 目標與上傳目的地, 確保兩者始終一致.
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

// gitlabUploader 負責上傳單一 version 的檔案, 並記錄 release 是否已存在,
// 以便在上傳時即時建立 asset link.
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
	// 先刪後傳以確保覆蓋的確定性: 先刪除同名 package file, 再上傳,
	// 使 registry 不留重複檔案 (見 CLAUDE.md).
	if err := u.g.deletePackageFile(u.version, file.name); err != nil {
		return err
	}
	headers := map[string]string{"Content-Type": contentType(file.name)}
	status, body, err := u.g.req("PUT", u.g.byNameURL(u.version, file.name), headers, bytes.NewReader(data))
	if err != nil {
		return err
	}
	if !ok2xx(status) {
		return statusError("上傳 "+file.name, status, body)
	}
	// 若 release 已存在, 立即建立 by-name link; 否則由 release create 事後補建.
	if u.hasRelease {
		return u.g.createLink(u.version, file.name)
	}
	return nil
}

// deletePackageFile 刪除指定 version 的 generic package 中所有同名的 package file
// (首次上傳時不存在同名檔案). 刪除需要較高權限; 403 會直接回傳錯誤 (見 CLAUDE.md).
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
			return statusError("刪除既有 package file "+name, status, body)
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
			return nil, fmt.Errorf("release %q 不存在", version)
		}
		if !ok2xx(status) {
			return nil, statusError("GET release "+version, status, body)
		}
		if err := json.Unmarshal(body, &rel); err != nil {
			return nil, fmt.Errorf("解析 release 回應: %w", err)
		}
	}
	// 以 rel.TagName 作為 generic package 版本 ("latest" 時 version 參數本身非實際 tag).
	return g.linkAssets(rel.TagName, rel.Assets.Links), nil
}

// latestRelease 回傳最新的非 upcoming release. GitLab 以最新在前的順序列出 release;
// "latest" 排除 upcoming release (相當於 GitHub 排除 draft / prerelease 的邏輯).
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
	return nil, fmt.Errorf("找不到已發布的 release")
}

func (g *gitlabPlatform) download(a asset, w io.Writer) error {
	// link 目標為 by-name generic package 下載 URL;
	// auth headers 中的 token 授權私有 repo 的下載 (見 CLAUDE.md).
	return g.getStream(a.ref, nil, w)
}
