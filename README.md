# forgectl

`forgectl` 是一支跨平台的 release / asset 命令列工具, 以同一套指令查詢與操作 GitHub
與 GitLab 上儲存庫的 release 與 asset. 採 noun-verb 兩層結構 (`release`, `asset`),
設計上追求明確 (explicit), 確定 (deterministic), 精簡 (minimal).

## 特色

- **跨平台統一介面** — 以同一套 `release` / `asset` 指令操作 GitHub 與 GitLab,
  行為與定義在兩平台保持一致, 不依賴任一平台的單邊捷徑.
- **設定驗證** — `forgectl ping` 一次驗證遠端設定 (host / TLS / 認證) 是否正確: 先測連線
  (含自架站台與 `--insecure`), 有 token 時再測認證並回報認證身分.
- **release 查詢與發布** — 列出儲存庫所有 release (含 tag 對應的 commit hash 與 asset
  清單), 或為指定版本建立正式 release.
- **asset 上傳與下載** — 把本地檔上傳為某版本的 asset (release 不需先存在), 或以 glob
  樣式選取後下載.
- **確定性覆蓋** — `asset upload` 一律「先刪同名再上傳」, 不留 orphan 檔, 也不受平台
  「允許重複」設定影響.
- **明確的發布語意** — 沒有隱含預設: 新 tag 必須以 `--commit` 指定指向的 commit;
  `asset upload` 一律覆蓋, 不設 `--replace` / `--keep-exist` 之類的旗標對.
- **集中式認證** — 以固定位置的 credential 檔 (TOML / YAML / JSON) 管理 token, 可分
  host 設定; `--token` / `--token-file` / `--user` 旗標做欄位層級覆寫.
- **自架站台支援** — `--host` 指向 GitHub Enterprise 或自架 GitLab; `--insecure`
  支援使用自簽憑證的內網 host.
- **腳本友善** — `release list --json` 輸出結構化資料; `asset download` 在樣式無命中時
  仍以退出碼 0 結束, 便於串接腳本.

## 安裝

> 尚未發行預先建置的執行檔; 現階段請從原始碼建置. 需要 [Go](https://go.dev/dl/).

從原始碼建置:

```bash
git clone https://github.com/<owner>/forgectl.git
cd forgectl
go build -o forgectl .
```

或直接安裝至 `$GOBIN`:

```bash
go install github.com/<owner>/forgectl@latest
```

確認安裝結果:

```bash
forgectl --help
```

## 使用

### 認證設定

把 token 寫進固定位置的 credential 檔; `forgectl` 會自動讀取, 不需 `--credential`
旗標. 搜尋位置 (依序, Windows 為 `%AppData%\forgectl\`):

```
~/.config/forgectl/credential.toml
~/.config/forgectl/credential.yaml
~/.config/forgectl/credential.yml
~/.config/forgectl/credential.json
```

以 host 名稱為頂層鍵, 各 host 各自帶 token (TOML 範例):

```toml
["github.com"]
token = "ghp_xxxxx"

["gitlab.mycorp.com"]
token = "glpat_xxxxx"
user  = "deploy"
```

credential 檔含祕密, 建議權限設為 `0600`, 並加進 `.gitignore`. 完整格式 (扁平 / 階層
寫法, 三種格式, 解析優先序) 見 [`docs/credential.md`](docs/credential.md).

### 指定平台與儲存庫

`<repo>` 只寫 `owner/repo` 路徑 (GitLab 可含子群組); 平台與 host 由旗標決定:

- `--source github|gitlab` (必填) — 指定平台, 不自動猜測.
- `--host URL` (選填) — 自架站台的 base URL; 省略即公開站 (github.com / gitlab.com).
- `--insecure` (選填) — 跳過 TLS 憑證驗證, 僅供使用自簽憑證的信任內網 host.

### 指令一覽

```
forgectl ping
forgectl release list     <repo> [--json]
forgectl release create   <repo> <version> (--note STR | --note-file PATH) [--commit COMMIT]
forgectl asset   upload   <repo> <version> <path>[=NAME]...
forgectl asset   download <repo> <version> [pattern]... [-d DIR] [-o NAME] [--overwrite]
```

### 驗證設定

設定好認證後, 可用 `ping` 確認遠端設定是否正確 (不需 `<repo>`):

```bash
# 公開 GitHub (匿名亦可, 僅測連線)
forgectl ping --source github

# 自架 GitLab, 使用自簽憑證
forgectl ping --source gitlab --host https://gitlab.mycorp.com --insecure
```

`ping` 先測連線 (含 TLS), 有 token 時再測認證並回報認證身分; 任一項失敗即以非 0 退出.

### 列出 release

```bash
# 公開 GitHub
forgectl release list nexgus/hgsystem --source github

# 自架 GitLab, 輸出 JSON 供腳本使用
forgectl release list team/proj --source gitlab --host https://gitlab.mycorp.com --json
```

### 上傳 asset

`release` 不需先存在; `<version>` 是貫穿各指令的 join key. 以 `path=NAME` 可指定上傳後
的 asset 名 (省略則取檔名):

```bash
forgectl asset upload nexgus/hgsystem v1.2.3 --source github \
  dist/app-linux=app \
  dist/checksums.txt
```

同名既有 asset 一律覆蓋. 多檔會逐一全部嘗試, 結束時彙總成功與失敗清單.

### 建立正式 release

把某版本已上傳的 asset 一併發布. 若 tag 尚不存在, 須以 `--commit` 明確指定指向的
commit (可為 SHA, 或特殊值 `latest` = 預設分支最新 commit):

```bash
# tag 已存在: 直接發布, note 行內提供
forgectl release create nexgus/hgsystem v1.2.3 --source github --note "first public release"

# tag 不存在: 以預設分支最新 commit 建 tag, note 從檔讀
forgectl release create nexgus/hgsystem v1.2.3 --source github \
  --note-file CHANGELOG.md --commit latest
```

### 下載 asset

以 glob 樣式選取 asset (記得替萬用字元加引號); `<version>` 可用 `latest` 取最新正式
release:

```bash
# 抓某版全部 asset 到目前目錄
forgectl asset download nexgus/hgsystem v1.2.3 --source github

# 只抓 linux 相關, 放進 ./dl/ (保留原名)
forgectl asset download nexgus/hgsystem v1.2.3 '*linux*' -d ./dl/ --source github

# 抓最新版的單一檔, 改存成別的名字
forgectl asset download nexgus/hgsystem latest checksums.txt -o sums.txt --source github
```

更完整的語法, 各平台行為與注意事項見 [`docs/cli.md`](docs/cli.md).

## 授權

本專案以 MIT 授權釋出, 詳見 [LICENSE.md](LICENSE.md).
