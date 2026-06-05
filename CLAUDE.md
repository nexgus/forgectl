# forgectl — 專案說明 (CLAUDE.md)

`forgectl` 是一支 Go CLI, 透過 GitHub / GitLab 的 REST API 查詢與操作 repo 的 release
與 asset. CLI 骨架 (kong 解析 + dispatch 與全部指令的宣告, 見 `pkg/cli`; `cmd/forgectl`
僅是把 `os.Args` 交給 `cli.Run` 的薄進入點) 與全部指令皆已實作:
`ping` (連線與 credential 解析), `release list` / `release create`, `asset upload` /
`asset download`. 設計文件在 `docs/`.

各指令的跨平台 REST 邏輯收在 `platform` 介面後 (`pkg/forge/forge.go`), 兩個實作分別在
`github.go` / `gitlab.go`; 平台無關的編排 (輸出格式, glob 比對, 本地檔 I/O, 上傳的逐檔
彙總) 在 `releases.go` / `assets.go`; 共用的 HTTP 與分頁工具在 `httpx.go`. HTTP 一律走
標準庫 `net/http` (與 `ping` 一致, 不引入 resty).

- `docs/cli.md` — CLI 語法與平台行為 (使用者導向; 只放語法與「在平台上做什麼」).
- `docs/credential.md` — credential 檔格式 (TOML / YAML / JSON), 認證的單一事實來源.

本檔記錄**實作層面**的內容: 設計原則, 參考實作, 跨平台模型的細節機制, 與待決的實作決策.

## 設計原則

explicit / deterministic / minimal: 明確優於隱含, 一致優於單邊捷徑, 表面越小越好.
例如 `asset upload` 一律覆蓋 (無 `--replace`); `release create` 的新 tag 必須以
`--commit` 明確指定 (不設隱含預設); 跨平台一律走同一套方法, 不依賴單邊捷徑.

## 參考實作

- `~/myproj/hgsystem/hgsys/pkg/repo/` — GitHub releases via REST.
  - `GetAllReleases` (`github.go`): 取 releases, 再**抓全部 tag** 建 `tagToSHA` /
    `shaToTags`, 用來填 commit hash 與 sibling tags. forgectl **刻意不同**: 只解析有
    release 的 tag (見下).
  - `cmd/check/main.go`: `release list` 文字輸出格式的範本.
- `~/myproj/techgit` — GitLab 工具; 用 **kong** (CLI parsing) + **resty** (HTTP),
  做 generic package upload / download. forgectl CLI 骨架的範本.

## 跨平台 release / asset 模型 (細節)

`<version>` 是貫穿各指令的 join key. `asset upload` 不需 release 先存在;
`release create` 把該 version 正式發布.

### GitLab — asset = generic package (+ release asset link)

- **asset upload**: `PUT` generic package file `(package_name=repo, version, file_name)`;
  不需 release.
- **覆蓋 = 先刪同名再上傳** (deterministic): 上傳前先查同名 package file, 有就刪再傳.
  好處: 不受專案「允許重複」設定影響, 不留 orphan, 與 GitHub 同一套邏輯.
- 刪除 package file 需較高權限 (官方文件: 無權限, 或受「保護套件」規則保護時回 403).
  首次上傳無同名可刪, 不受影響; 重傳需刪除權限, 不足即報錯結束.
- 若該 version 的 release 已存在, 建一個 asset link 指向 **by-name 下載 URL** (穩定;
  該 name 的 link 已存在則沿用). link 的 `name` / `url` 在同一 release 內須唯一.
- **release create**: tag 已有 Release → 報錯; 否則建 Release (tag = `<version>`,
  不存在時以 `ref` = `--commit` 指定的 commit, `latest` 取預設分支最新), 再**自動反查**
  該 version 的 package files 連成 asset link.
  - **反查方式**: 查 registry — 列出 `(package_type=generic, package_name=repo)` 的
    package, 比對出 `version` 相符者, 取其 package files 清單; 不需使用者重列.
  - **delete-then-upload 保證 1:1**: 每個檔名在 registry 只有一份, 反查時是乾淨的一檔
    一 link, 不會出現「同名多份, 不知連哪個」.
  - link 以 name 唯一, 已存在則沿用; 與 `asset upload` 時機補的 link 殊途同歸 —
    upload / create 不論先後, 最終 link 集合一致.
- **asset download**: 列出該 release 的 asset link, 依名稱 (glob) 比對, 從 link 指向的
  by-name generic package 下載 URL 取檔; release 的 sources 原始碼壓縮檔不納入比對.
- 參考: generic package 預設允許重複, 下載 by name 取最新 (DB id 最大者); 我們因
  「先刪再傳」而不依賴此行為.

### GitHub — asset = release asset

- **asset upload**: 該 tag 無 release 時自動建一個 draft release (對外不可見, tag 也可
  暫不存在, 等於 staging 區) 來掛 asset. 同名上傳原生報 `already_exists`, 故先刪掉既有
  同名 asset 再傳.
- **release create**: tag 已有正式 release → 報錯; 只有 draft → 轉為正式 (publish) 並
  寫入 notes; 皆無 → 新建. publish 時 tag 不存在則依 `target_commitish` (`--commit`,
  `latest` = 預設分支最新) 建立.
- **asset download**: 列出 release assets, 依名稱 (glob) 比對, 透過 release asset API
  端點 (`GET /repos/.../releases/assets/:id`, `Accept: application/octet-stream`) 下載,
  而非 `browser_download_url` (才能在私有 repo 帶 token 下載).

### api_base 推導

由 `--source` + `--host` 依慣例算出 (cli.md 有對照表). 重點: GitLab 一律
`{host}/api/v4`; GitHub 不對稱 — 公開站是 `https://api.github.com`, 自架 (GitHub
Enterprise) 才是 `{host}/api/v3`.

## release list 的 commit hash 解析

- 顯示每筆 release 的 tag 所指向的 commit. **方法兩平台一致**: 只解析**有 release 的
  那幾個 tag** (逐一解析, 不抓全部 tag). GitLab 雖在 release 物件內附 commit, 仍一律改
  以解析 tag 取得, 確保兩平台行為與定義相同.
- **不顯示 sibling tags** (指向同一 commit 的其他 tag): 需先抓全部 tag 才算得出, 與
  「只解析有 release 的 tag」衝突, 故捨棄.

## ping (設定驗證)

`ping` 是第一個完整實作的指令, 順帶把各指令共用的基礎建設先做起來:
`pkg/forge/endpoint.go` (api_base / credential host 推導), `pkg/forge/credential.go`
(credential 檔搜尋 / 解析 / 旗標覆寫優先序), `pkg/forge/ping.go` (HTTP 與分層檢查).

- **分層檢查**: (1) 連線 — 對 api_base 發**不需 token** 的請求 (GitHub: API root; GitLab:
  `/version`), 取得任何 HTTP 回應 (即使錯誤狀態碼) 即算連線成功, 只有傳輸層錯誤 (含 TLS
  憑證) 才算失敗; 故沒 token 也能驗證 `--host` / `--insecure` / TLS. (2) 認證 — 有 token 時
  打 `/user` 回報身分; 無 token 略過 (匿名為合法用法).
- **認證 header**: GitHub `Authorization: Bearer` (另帶 `X-GitHub-Api-Version`); GitLab
  `PRIVATE-TOKEN`. 身分欄位: GitHub `login`, GitLab `username`.
- **GitLab deploy token 的限制**: deploy token 無法存取 `/user`, 故 `ping` 對它一律回報
  401 認證失敗 (已選擇此分層做法, 而非繞過 `/user`); 文件註明改以實際 asset 操作驗證.
- **不印 token 本體**: 只印 token 來源 (`--token` / `--token-file` / credential 檔路徑 / none),
  避免祕密外洩到終端機或日誌.
- credential 檔權限可被他人讀取時印警告至 stderr; `--insecure` 亦印警告.

## self install / uninstall (本機安裝)

`self` 是 "對 forgectl 自身操作" 的命令群, 把下載的 `forgectl-<版本>-<os>-<arch>[.exe]`
安裝成可在任何位置以 `forgectl` 執行 (使用者導向語法見 `docs/cli.md` 的 self 章節). 它**只
碰本機檔案系統與 PATH, 與 GitHub / GitLab 無關**, 故實作獨立於 `pkg/forge` 之外, 收在
`pkg/selfinstall/`, 以 build tag 分平台 (`selfinstall_unix.go` / `selfinstall_windows.go`,
共用層在 `selfinstall.go`, macOS 隔離屬性在 `quarantine_darwin.go` / 其他平台 no-op).

- **命名空間 + 累積模型**: vendor 目錄為 `<root>/augustus.sanchung/forgectl/`
  (`root` = `/opt` 或 `%ProgramFiles%`). install 把自己複製進去, 故改版多次會累積多個版本檔;
  穩定入口 `forgectl` 永遠指向**本次**安裝的版本檔. uninstall 刪整個 `.../forgectl/`, 其上的
  `augustus.sanchung` 層**僅在已空時**收掉 (可能與其他工具共用), **永不刪 `/opt` 或
  `Program Files`**.
- **落點檔名由編譯期事實構造, 不信任磁碟檔名** (`canonicalName`, 見 `selfinstall.go`): 落點名
  固定為 `forgectl-<version.String>-<GOOS>-<GOARCH>[.exe]`, 與 `build.sh` 產物逐字一致, **不取
  `os.Executable()` 的 basename**. 否則經由已安裝入口重跑時, basename 會是入口名而非版本名:
  symlink (Linux / macOS) 雖可用 `EvalSymlinks` 跟隨回版本檔, 但 **hard link (Windows) 沒有可
  跟隨的目標** (它本身就是該 inode 的對等檔名, `forgectl.exe` 已經 "是檔案", 名字卻不對), 故
  "follow link 直到是檔案" 對 hard link 無效. 改以編譯期事實構造名稱, 一併解決 symlink /
  hard link 執行與使用者改名下載檔三種情形; `os.Executable()` 仍用來取**複製來源的位元組**
  (它定義上就是正在執行的 forgectl, 版本 / 平台 / 架構無從造假).
- **連結方式刻意依平台分開挑** (使用者只要求 "cmd 下能跑 `forgectl`", 兩者皆滿足):
  - **Linux / macOS = symlink** `/usr/local/bin/forgectl` → vendor 內版本檔. 用 symlink 是
    因入口 (`/usr/local/bin`) 與 vendor (`/opt`) 可能分屬不同掛載點, hard link 會踩
    `EXDEV` (cross-device link). uninstall 只移除**確實指向本工具 vendor 目錄**的 symlink
    (`os.Readlink` + 前綴比對), 避免誤刪同名的他人安裝; 是一般檔或指向別處則保留並警告.
  - **Windows = hard link** `forgectl.exe` → 同目錄版本檔 (皆在 vendor 目錄內). 同目錄必同
    volume, hard link 成立且免 symlink 特權; cmd 打 `forgectl` 由 PATHEXT 解析 `forgectl.exe`.
- **入口上 PATH 的方式**: Linux / macOS 靠 `/usr/local/bin` 預設即在 PATH, 故不在時**只警告,
  不擅改使用者 shell rc** (避免 sudo 下 `$HOME` 變 root 家目錄等坑). Windows 無對應的預設
  系統 bin 目錄, 故把 vendor 目錄加入**機器層級 PATH** (`reg add` 寫
  `HKLM\...\Session Manager\Environment`, 變更需重開終端機生效).
- **macOS 專屬**: 清 `com.apple.quarantine` (`xattr -p` 探測再 `-d`, 屬性不存在視為成功),
  否則 Gatekeeper 擋未簽章 binary; **不可用 `/usr/sbin`** (SIP 保護不可寫), 一律 `/usr/local/bin`.
- **權限**: install / uninstall 皆需提權, 開頭先檢查 (Unix `os.Geteuid()`; Windows
  `shell32!IsUserAnAdmin`), 不足即提早報錯. Windows 端**不引入新依賴**: PATH 走 `reg`,
  admin 檢查走 `syscall` lazy DLL, hard link 走 `os.Link`.
- **退出碼**: install 任一步失敗即中止回非 0; uninstall 個別失敗只警告不中止, 期間有錯
  (含保留外來入口) 最終回非 0 - 與其他指令的彙總式退出一致.
- **`--source` 的豁免**: `Source` 旗標原以 kong `enum` + `required` 守門, 會連 self 也強制
  要求 `--source`. 改為移除該兩個 tag, 在 root 的 `Validate(kctx *kong.Context)` 手動驗證:
  非 self 指令才要求 `--source` 為 github 或 gitlab, self 指令 (及無選定指令) 豁免. 直接以
  結構建構並呼叫 `Run` 的測試不經 kong 解析, 不觸發此驗證, 不受影響.

## 實作決策

- **credential 檔解析套件** (隨 `ping` 實作確定): TOML = `github.com/BurntSushi/toml`,
  YAML = `gopkg.in/yaml.v3`, JSON = 標準庫 `encoding/json`. 解析時一律先 unmarshal 進
  `map[string]any`, 再判定扁平 / 階層 (頂層出現 map 值 = 階層, 出現 `token` / `user` 字串
  = 扁平, 兩者並存即報錯); 見 `pkg/forge/credential.go`.
- **`<version>` 位置參數的 semver 守門**: `<version>` 是貫穿各指令的 join key, 漏填時後面
  的 `<path>` / `<pattern>` 會被當成版本而誤觸操作. 故把版本欄位宣告為自訂型別 (`Version`
  / `VersionOrLatest`, 見 `cmd/forgectl/versionarg.go`), 由 kong 在**解析階段**呼叫其
  `Validate` 做守門, 在送出任何請求前放棄執行 (錯誤連同 usage 一併印出, 直接點出可能漏填).
  - 驗證委由 `golang.org/x/mod/semver` (Go 團隊維護). 該模組要求 `v` 前綴, 故缺前綴時先
    補 `v` 再驗證, 以同時接受 `v1.2.3` 與 `1.2.3`; **不改寫**使用者輸入的 tag, 僅判定真偽.
  - `release create` / `asset upload` 用 `Version` (嚴格 semver); `asset download` 用
    `VersionOrLatest`, 額外接受特殊值 `latest`. 直接以結構建構並呼叫 `Run` 的測試 (不經
    kong 解析) 不觸發此守門, 不受影響.
- **GitLab package link 下載一律重建 by-name API URL** (`linkAssets`, 見 `pkg/forge/gitlab.go`):
  GitLab 會把 `link_type=package` 的 asset link `url` / `direct_asset_url` 正規化成 **web
  permalink** `/<repo>/-/package_files/:id/download`. 該 web 路由只認瀏覽器 session,
  帶 `PRIVATE-TOKEN` (header 或 `?private_token=` query 皆然) 一律被 `302` 導向
  `/users/sign_in`, 於是整頁登入 HTML 被當成 asset 存出 (實測症狀: 下載到的 `.whl` 其實是
  登入頁, `pip install` 報 `Wheel ... is invalid`). 故下載 **不信任 link 存的 url**: 對
  `link_type=package` 的 asset, 以 `(pkgName, version=tag, name)` 重建 by-name generic
  package API URL (`byNameURL`, 與上傳目的地同一個 URL) 下載 — 它接受 `PRIVATE-TOKEN`,
  回 `application/octet-stream`. 非 package 類型 (外部 url) 才沿用 link 的 url.
  - 這也修正 forgectl 自身 upload→download 來回流程的問題: `createLink` 雖存 API URL,
    GitLab 仍會將其改寫成 `/-/package_files/:id/download`, 故不重建即觸發同一問題.
  - 前提假設: generic package name = repo 末段 (`g.pkgName`), package version = release tag —
    與 upload 端一致 (CLAUDE.md 跨平台模型). 不成立時 API 端點回 404 明確報錯, 而非默默存下
    損壞的檔案.
- **下載登入頁守門** (`getStream`, 見 `pkg/forge/httpx.go`): 上一點為正解, 此為縱深防禦 —
  若仍有下載落入登入頁 (其他 link 類型或別種認證失敗), 在串流寫出前, 若**跟隨重導後的
  最終網址落在登入頁** (GitLab `/users/sign_in`, GitHub `/login` / `/session`) **且**回應為
  HTML, 即報錯, 而非將登入頁 HTML 默默寫出檔案.
  - **刻意不檢查 asset 的內容格式 / magic number**: asset 可為任意格式 (含合法的 HTML 檔),
    以格式判定會誤殺. 改用「最終網址是否為登入頁」這個與 asset 內容無關的特徵; HTML
    Content-Type 僅作搭配條件, 不單獨作為判據. 正常下載端點不會從登入頁供檔, 故不誤殺.
