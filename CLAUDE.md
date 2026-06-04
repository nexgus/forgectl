# forgectl — 專案說明 (CLAUDE.md)

`forgectl` 是一支 Go CLI, 透過 GitHub / GitLab 的 REST API 查詢與操作 repo 的 release
與 asset. CLI 骨架 (kong 解析 + dispatch, 見 `cmd/forgectl`) 與全部指令皆已實作:
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

## 實作決策

- **credential 檔解析套件** (隨 `ping` 實作確定): TOML = `github.com/BurntSushi/toml`,
  YAML = `gopkg.in/yaml.v3`, JSON = 標準庫 `encoding/json`. 解析時一律先 unmarshal 進
  `map[string]any`, 再判定扁平 / 階層 (頂層出現 map 值 = 階層, 出現 `token` / `user` 字串
  = 扁平, 兩者並存即報錯); 見 `pkg/forge/credential.go`.
