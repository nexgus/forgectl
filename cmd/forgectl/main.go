// Command forgectl 透過 GitHub 與 GitLab 的 REST API 查詢與操作 release 和 asset.
//
// 本檔僅是進入點: 把命令列引數交給 pkg/cli, 並以其回傳值作為行程結束碼.
// 完整的命令列介面 (語法, 旗標, 分派) 定義在 pkg/cli.
package main

import (
	"os"

	"forgectl/pkg/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}
