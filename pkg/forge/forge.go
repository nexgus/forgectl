// Package forge 負責各指令背後的實際 GitHub / GitLab REST 工作. 它所需的一切
// 均以參數傳入 — 連線與認證設定透過 New, 各指令的輸入透過各方法 — 因此不依賴任何
// CLI 型別, 也能獨立進行測試.
//
// 共用的連線與 credential 解析位於 endpoint.go 與 credential.go; 平台無關的
// 指令編排位於 releases.go 與 assets.go; 各 source 在 platform 介面後的 REST
// 實作分別位於 github.go 與 gitlab.go. ping.go 是第一個指令, 同時驗證相同的
// 連線與 credential 解析流程.
package forge

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// Config 攜帶 Client 所需的已解析全域設定. cmd/forgectl 從解析後的全域旗標建立
// 此結構, 使 forge 不依賴任何 CLI 型別 (且可平行測試, 因為不讀取共享全域狀態).
type Config struct {
	Source    string // "github" 或 "gitlab"
	Host      string // 自架實例的 base URL; 公開站台時留空
	Insecure  bool   // 略過 TLS 憑證驗證
	Token     string // token 覆寫值
	TokenFile string // 讀取 token 的檔案路徑
	User      string // 使用者覆寫值
}

// Client 與一個 hosting platform 通訊, 由 Config 設定.
type Client struct {
	cfg Config
}

// New 回傳以指定設定建立的 Client.
func New(cfg Config) *Client {
	return &Client{cfg: cfg}
}

// platform 將各 source (GitHub / GitLab) 的 REST 操作抽象化, 置於 release 與
// asset 指令的介面後. Client 依 Config 建立對應實作; 指令方法負責編排 platform
// 呼叫, 並擁有橫切關注點 (輸出格式、glob 比對、本地檔案 I/O, 以及上傳時的逐檔
// 成功/失敗統計).
type platform interface {
	// listReleases 回傳所有 release, 每筆的 commit 皆從該 release 本身的 tag 解析
	// 取得 (只解析有 release 的 tag, 逐一解析).
	listReleases() ([]release, error)

	// createRelease 以指定 note 為 version 發布一個 release. commit 指定新 tag
	// 所指向的 commit (SHA 或 "latest" 代表預設分支的最新 commit); 只有 tag
	// 尚不存在時才必填.
	createRelease(version, note, commit string) error

	// newUploader 為 version 準備上傳目標 — GitHub 為取得或新建的 draft release,
	// GitLab 為已解析的 project 與 package — 使多個檔案可重複使用同一次準備.
	newUploader(version string) (uploader, error)

	// findReleaseAssets 將 version (允許 "latest") 解析為其 asset 清單,
	// 若對應 release 不存在則回傳錯誤.
	findReleaseAssets(version string) ([]asset, error)

	// download 將 asset 的位元組串流寫入 w.
	download(a asset, w io.Writer) error
}

// uploader 是已準備好的上傳目標, 使多個檔案可重複使用同一次準備
// (GitHub 為一次 get-or-create release, GitLab 為一次 project / package 解析).
type uploader interface {
	// upload 將一個本地檔案寫入為 asset, 並覆蓋同名的既有 asset.
	upload(file localAsset) error
}

// platform 解析 repo 的連線與 credential, 輸出共用警告, 並建立對應 source 的
// platform 實作.
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
		return nil, fmt.Errorf("不支援的 source %q", c.cfg.Source)
	}
}

// emitWarnings 印出指令在執行工作前顯示於 stderr 的共用警告: credential 檔權限
// 警告 (在 credential 解析期間收集) 以及 --insecure 提示. Ping 以行內方式印出
// 相同的警告集合.
func emitWarnings(a auth, insecure bool) {
	for _, w := range a.Warnings {
		fmt.Fprintln(os.Stderr, "警告: "+w)
	}
	if insecure {
		fmt.Fprintln(os.Stderr, insecureWarning)
	}
}

// insecureWarning 是 TLS 驗證關閉時顯示於 stderr 的提示.
const insecureWarning = "警告: TLS 憑證驗證已停用 (--insecure), 請僅在受信任的 host 上使用"

// splitRepo 將 GitHub 的 "owner/repo" 路徑拆分為兩個部分. GitLab 路徑可能包含
// subgroup, 由 GitLab platform 自行處理.
func splitRepo(repo string) (owner, name string, err error) {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("repo 必須為 \"owner/repo\" 格式, 實際收到 %q", repo)
	}
	return parts[0], parts[1], nil
}
