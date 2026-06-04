# forgectl — 專案說明 (CLAUDE.md)

`forgectl` 是一支 Go CLI, 透過 GitHub / GitLab 的 REST API 查詢與操作 repo 的 release
與 asset. 目前處於**設計階段**, 尚無程式碼; 設計文件在 `docs/`.

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

## 待決的實作決策

- 解析 TOML / YAML / JSON 的相依套件選擇 (實作時定).
