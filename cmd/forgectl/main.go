// Command forgectl 透過 GitHub 與 GitLab 的 REST API 查詢與操作 release 和 asset.
//
// 本檔定義命令列介面 (docs/cli.md 描述的名詞-動詞語法), 並透過 kong 的 Run 方法
// 分派執行. 每個指令的 Run 從全域旗標建立 pkg/forge client, 再把指令本身的欄位傳給它;
// 實際工作在 pkg/forge 內完成, 它只接受傳入的參數, 不讀取任何全域狀態.
package main

import (
	"os"

	"github.com/alecthomas/kong"

	"forgectl/pkg/forge"
	"forgectl/pkg/version"
)

// Globals 是所有指令共用的旗標, 對應 docs/cli.md 的 "global flags" 與 "authentication" 章節.
type Globals struct {
	Source    string `short:"s" enum:"github,gitlab" required:"" help:"托管平台: github 或 gitlab."`
	Host      string `short:"H" placeholder:"URL" help:"自架實例的 base URL; 使用公開站時省略."`
	Insecure  bool   `short:"k" help:"略過 TLS 憑證驗證; 僅適用於具有自簽憑證的受信任自架 host."`
	Token     string `help:"覆寫 token."`
	TokenFile string `type:"path" placeholder:"PATH" help:"從檔案讀取 token 並覆寫."`
	User      string `help:"覆寫使用者名稱."`

	Version kong.VersionFlag `short:"V" help:"印出版本資訊後離開."`
}

// client 從全域旗標建立 forge client. 每個 Run 方法都呼叫它,
// 使 forge 不依賴任何 CLI 型別, 也不讀取共用狀態.
func (g *Globals) client() *forge.Client {
	return forge.New(forge.Config{
		Source:    g.Source,
		Host:      g.Host,
		Insecure:  g.Insecure,
		Token:     g.Token,
		TokenFile: g.TokenFile,
		User:      g.User,
	})
}

// CLI 是指令樹的根節點.
type CLI struct {
	Globals

	Ping    PingCmd    `cmd:"" help:"驗證遠端設定 (host, TLS 及 credential) 是否正確."`
	Release ReleaseCmd `cmd:"" help:"查詢與管理 release."`
	Asset   AssetCmd   `cmd:"" help:"上傳與下載 asset."`
}

// PingCmd 實作: forgectl ping
//
// 不接受位置參數: ping 驗證連線與 credential (全域旗標), 而非特定 repo.
type PingCmd struct{}

func (c *PingCmd) Run(g *Globals) error {
	return g.client().Ping()
}

// ReleaseCmd 彙整 "release" 子指令.
type ReleaseCmd struct {
	List   ReleaseListCmd   `cmd:"" help:"列出 repo 的所有 release."`
	Create ReleaseCreateCmd `cmd:"" help:"為某個版本發布 release, 並掛載已上傳的 asset."`
}

// AssetCmd 彙整 "asset" 子指令.
type AssetCmd struct {
	Upload   AssetUploadCmd   `cmd:"" help:"將一或多個本地檔案以 asset 形式上傳至某版本."`
	Download AssetDownloadCmd `cmd:"" help:"下載 release 的 asset, 可選擇以 glob 篩選."`
}

// ReleaseListCmd 實作: forgectl release list <repo> [--json]
type ReleaseListCmd struct {
	Repo string `arg:"" name:"repo" help:"目標 repo, 格式為 owner/repo."`
	JSON bool   `help:"輸出 JSON 供程式處理, 而非人類可讀的文字格式."`
}

func (c *ReleaseListCmd) Run(g *Globals) error {
	return g.client().ReleaseList(c.Repo, c.JSON)
}

// ReleaseCreateCmd 實作:
// forgectl release create <repo> <version> (--note STR | --note-file PATH) [--commit COMMIT]
type ReleaseCreateCmd struct {
	Repo     string  `arg:"" name:"repo" help:"目標 repo, 格式為 owner/repo."`
	Version  Version `arg:"" name:"version" help:"Release tag (例如 v1.2.3); tag 不存在時依 --commit 建立."`
	Note     string  `xor:"note" required:"" help:"Release note 文字."`
	NoteFile string  `short:"n" xor:"note" required:"" type:"path" placeholder:"PATH" help:"從檔案讀取 release note (整個檔案即為 note 內容)."`
	Commit   string  `short:"c" placeholder:"COMMIT" help:"新 tag 所指向的 commit: 一個 commit SHA, 或 'latest' 代表預設分支的 HEAD. 僅在 tag 尚不存在時必填."`
}

func (c *ReleaseCreateCmd) Run(g *Globals) error {
	return g.client().ReleaseCreate(c.Repo, string(c.Version), c.Note, c.NoteFile, c.Commit)
}

// AssetUploadCmd 實作: forgectl asset upload <repo> <version> <path>[=NAME]...
type AssetUploadCmd struct {
	Repo    string   `arg:"" name:"repo" help:"目標 repo, 格式為 owner/repo."`
	Version Version  `arg:"" name:"version" help:"版本字串; release 不需預先存在."`
	Paths   []string `arg:"" name:"path" help:"一或多個本地檔案, 每個可加上 =NAME 後綴以重新命名上傳的 asset."`
}

func (c *AssetUploadCmd) Run(g *Globals) error {
	return g.client().AssetUpload(c.Repo, string(c.Version), c.Paths)
}

// AssetDownloadCmd 實作:
// forgectl asset download <repo> <version> [pattern]... [-d DIR] [-o NAME] [--overwrite]
type AssetDownloadCmd struct {
	Repo      string          `arg:"" name:"repo" help:"目標 repo, 格式為 owner/repo."`
	Version   VersionOrLatest `arg:"" name:"version" help:"Release tag, 或 'latest' 代表最新已發布的 release."`
	Patterns  []string        `arg:"" name:"pattern" optional:"" help:"與 asset 名稱比對的 glob pattern; 省略則下載所有 asset."`
	Dir       string          `short:"d" type:"path" placeholder:"DIR" help:"下載目標目錄; 不存在時自動建立. 預設為當前目錄."`
	Output    string          `short:"o" placeholder:"NAME" help:"輸出檔名; 僅在下載單一 asset 時有效."`
	Overwrite bool            `help:"若目標檔案已存在則覆寫."`
}

func (c *AssetDownloadCmd) Run(g *Globals) error {
	return g.client().AssetDownload(c.Repo, string(c.Version), c.Patterns, c.Dir, c.Output, c.Overwrite)
}

// newParser 建立綁定至 target 的 kong parser. main 將它綁定至已解析的 CLI;
// 測試則綁定至全新的 CLI, 以隔離測試語法.
func newParser(target *CLI) (*kong.Kong, error) {
	return kong.New(
		target,
		kong.Name("forgectl"),
		kong.Description("透過 GitHub 與 GitLab 查詢與操作 release 和 asset."),
		kong.UsageOnError(),
		kong.Vars{"version": version.Full()},
	)
}

func main() {
	var cli CLI
	parser, err := newParser(&cli)
	if err != nil {
		panic(err)
	}
	ctx, err := parser.Parse(os.Args[1:])
	parser.FatalIfErrorf(err)

	// kong 分派至所選指令的 Run 方法, 並注入其所需的全域旗標.
	ctx.FatalIfErrorf(ctx.Run(&cli.Globals))
}
