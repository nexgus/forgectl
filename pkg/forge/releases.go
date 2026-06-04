package forge

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// release 是單一 release 的標準化跨平台視圖. 平台沒有對應概念的欄位為 nil 指標
// (GitLab 無 draft / prerelease; GitHub 無 upcoming), 使 release list 的文字輸出
// 與 --json 輸出能在 docs/cli.md 規定之處正確輸出 null.
type release struct {
	Name       string
	Tag        string
	Draft      *bool // GitHub only
	Prerelease *bool // GitHub only
	Upcoming   *bool // GitLab only
	Commit     string
	Assets     []asset
}

// asset 是單一可下載 asset 的標準化視圖. Size 在平台未回報大小時為 nil
// (GitLab asset link 不帶大小). ref 是平台專屬的下載句柄
// (GitHub asset API URL 或 GitLab link URL), 不納入任何輸出.
type asset struct {
	Name string
	URL  string
	Size *int64
	ref  string
}

// ReleaseList 實作: forgectl release list <repo> [--json]
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

// ReleaseCreate 實作:
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
	fmt.Printf("release %s 已建立\n", version)
	return nil
}

// noteText 從行內 --note 或 --note-file 路徑解析 release 備註;
// CLI 強制恰好提供其中一個. --note-file 讀取整個檔案作為備註
// (同 --token-file, 不支援 stdin "-").
func noteText(note, noteFile string) (string, error) {
	if noteFile != "" {
		data, err := os.ReadFile(noteFile)
		if err != nil {
			return "", fmt.Errorf("讀取 --note-file %s 失敗: %w", noteFile, err)
		}
		return string(data), nil
	}
	return note, nil
}

// printReleasesText 輸出 docs/cli.md 規定的人可讀 release 清單.
// 不顯示總筆數, 各項目以空行分隔.
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

// releaseLabels 輸出 release tag 行的狀態後綴: GitHub 的
// (draft) / (prerelease), GitLab 的 (upcoming). 一般已發布的 release 無後綴.
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

// releaseJSON / assetJSON 是 docs/cli.md 規定的 --json 輸出結構.
// 指標欄位在平台無對應值時序列化為 null.
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

// printReleasesJSON 將 release 清單以 docs/cli.md 規定的 JSON 陣列格式寫出.
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

// shortCommit 將 commit SHA 縮短為前七個字元, 供文字清單顯示使用.
func shortCommit(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// humanSize 將位元組數轉換為人可讀的大小字串 (B, KiB, MiB, ...),
// 格式與 docs/cli.md 的 release-list 輸出一致.
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
