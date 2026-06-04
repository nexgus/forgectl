# forgectl CLI 設計

`forgectl` 透過各程式碼託管平台的 REST API 查詢與操作儲存庫的 release 與
asset. 目前規劃支援 **github**, **gitlab** 兩種 source.

採 noun-verb 兩層結構, 目前有 `release` 與 `asset` 兩個 noun; 另有一個獨立的診斷指令
`ping` (無 noun-verb, 不接受 `<repo>`), 用來驗證連線與認證設定.

## 命令一覽

```
# 全域旗標 (各指令通用): --source github|gitlab (必填), [--host URL], [--insecure], 認證旗標
forgectl ping                                                 # 驗證設定 (連線 / TLS / 認證); 無 <repo>
forgectl release list     <repo> [--json]
forgectl release create   <repo> <version> (--note STR | --note-file PATH) [--commit COMMIT]
forgectl asset   upload    <repo> <version> <path>[=NAME]...
forgectl asset   download  <repo> <version> [pattern]...      [-d DIR] [-o NAME] [--overwrite]
```

`<repo>` 只是 **owner/repo 路徑**; 平台與 host 由全域旗標 `--source` / `--host` 決定.
每個指令的完整說明見下方各章節; `<repo>` 寫法, 認證, 跨平台行為等共用主題集中在後段
的 "共用主題".

---

# ping

驗證與遠端有關的設定是否正確: 解析連線目標與認證後, 做**分層檢查** — 先測連線 (含 TLS),
再測認證. `ping` 不接受 `<repo>`, 只驗證連線與認證本身, 與特定 repo 無關.

## 語法

```
forgectl ping
```

全域旗標: `--source` (必填), 另有 `--host` / `--insecure` 與認證旗標 (`--token` /
`--token-file` / `--user`); 見共用主題. `ping` 沒有自己的位置參數, 全部行為由全域旗標決定.

## 示範

```
# 以 credential 檔的認證測試公開 GitHub
forgectl ping --source github

# 測試自架 GitLab: token 從檔讀, host 使用自簽憑證
forgectl ping --source gitlab --host https://tech-git.nexcom.com.tw --token-file ~/.gl-token --insecure
```

## 注意事項

- **分層檢查, 依序兩層**:
  1. **連線**: 對 api_base 發一個不需 token 的請求 (GitHub: API root; GitLab: `/version`).
     只要取得 HTTP 回應 (即使是錯誤狀態碼) 即視為連線成功 — 這驗證 `--host`, `--insecure`
     與 TLS, **即使沒有任何認證也能測**. 傳輸層失敗 (DNS, 連線被拒, 憑證驗證失敗) 即連線失敗.
  2. **認證**: 解析到 token 時, 對該平台的 current-user 端點 (`/user`) 發認證請求, 確認
     token 並回報認證身分 (GitHub login / GitLab username). **未解析到 token 時略過此層**
     (匿名亦為合法用法).
- **只印 token 來源, 絕不印 token 本體**: 輸出先列出解析到的設定 (source / host / api_base /
  TLS 驗證 / token 來源 / user), 再列出各層結果.
- 認證帶法依平台 (見 §認證): GitHub 用 `Authorization: Bearer`; GitLab 用 `PRIVATE-TOKEN`.
- **GitLab deploy token 的限制**: deploy token 無法存取 `/user` (僅能用於 package
  registry), `ping` 會將其回報為認證失敗. 要驗證 deploy token, 請改以實際的 asset 操作測試.
- **退出碼**: 連線通過, 且 (有 token 時) 認證也通過 → 0; 連線失敗, 或 token 無效 / 過期 /
  權限不足 → 非 0. **無認證 (匿名) 但連線成功仍為 0**.
- credential 檔若可被其他使用者讀取, 印一行警告至 stderr (建議 `chmod 600`); `--insecure`
  亦印一行警告至 stderr.

## 輸出格式

```
# 公開 GitHub, 匿名 (無認證): 連線成功, 認證略過
Source:       github
Host:         github.com (public)
API base:     https://api.github.com
TLS verify:   on
Token:        none
User:         none

Connectivity: OK
Auth:         skipped (no credentials resolved)
```

```
# 自架 GitLab, 帶 token: 連線與認證皆成功, 回報身分
Source:       gitlab
Host:         tech-git.nexcom.com.tw (self-hosted)
API base:     https://tech-git.nexcom.com.tw/api/v4
TLS verify:   on
Token:        credential file (~/.config/forgectl/credential.toml)
User:         none

Connectivity: OK
Auth:         OK - authenticated as "augustus" (id 42)
```

---

# release list

列出 repo 的所有 release.

## 語法

```
forgectl release list <repo> [--json]
```

全域旗標: `--source` (必填), 另有 `--host` / `--insecure` (見共用主題).

## 參數

| 參數 | 必填 | 說明 |
|---|---|---|
| `<repo>` | ✅ | 目標 repo (owner/repo 路徑); 平台 / host 見 §`<repo>` 與 source / host. |
| `--json` | ⬜ | 改輸出 JSON (供腳本); 省略則為人看的文字格式. |

## 示範

```
forgectl release list hello/hgsystem --source github
forgectl release list team/proj --source gitlab --host https://gitlab.mycorp.com
```

## 注意事項

- GitHub / GitLab 皆走原生 Releases API 取得 release 清單.
- **列出全部** release, 不設 `--latest` (要最新版用 `asset download ... latest`).
- draft / prerelease **一律列出**, 不做過濾旗標, 改在每筆的**狀態標示**呈現:
  - **GitHub**: `(draft)` / `(prerelease)`; 兩者皆無即正式發布.
  - **GitLab**: 無 draft / prerelease 概念; `released_at` 在未來者標 `(upcoming)`.
- **不列自動產生的原始碼壓縮檔** (GitHub zipball / tarball, GitLab `assets.sources`),
  只列使用者上傳的 asset.
- asset 大小: **GitHub 有 size, GitLab 的 asset link 無 size** (API 不提供); 故 GitLab
  只顯示 asset 名與下載連結.
- **commit hash**: 顯示每筆 release 的 tag 所指向的 commit (解析方法見 CLAUDE.md).
- **不顯示 sibling tags** ("指向同一 commit 的其他 tag").

## 輸出格式

預設文字格式逐筆列出 release (**不顯示總數**):

```
# GitHub — asset 有 size
[1] first public release
    release tag: v1.2.3
    commit hash: a1b2c3d
    assets (2):
      - app-linux (8.4 MiB)
        https://github.com/hello/hgsystem/releases/download/v1.2.3/app-linux
      - checksums.txt (124 B)
        https://github.com/hello/hgsystem/releases/download/v1.2.3/checksums.txt

[2] (未命名)
    release tag: v1.3.0-rc1 (prerelease)
    commit hash: f00dcaf
    assets: 無
```

```
# GitLab — asset link 無 size; 無 draft / prerelease, 未來釋出標 (upcoming)
[1] v1.2.3
    release tag: v1.2.3
    commit hash: a1b2c3d
    assets (1):
      - app-linux
        https://gitlab.mycorp.com/api/v4/projects/<id>/packages/generic/proj/v1.2.3/app-linux
```

`--json` 輸出 release 陣列, 每筆欄位: `name`, `tag`, `draft`, `prerelease`,
`upcoming`, `commit`, `assets[] { name, url, size }`. 平台無對應的欄位以 `null` 表示
(GitLab 無 `draft` / `prerelease` / `size`; GitHub 無 `upcoming`).

---

# release create

為某 `<version>` 建立正式 release, 把該版本已上傳的 assets 一併發布.

## 語法

```
forgectl release create <repo> <version> (--note STR | --note-file PATH) [--commit COMMIT]
```

全域旗標: `--source` (必填), 另有 `--host` / `--insecure` (見共用主題).

## 參數

| 參數 | 必填 | 說明 |
|---|---|---|
| `<repo>` | ✅ | 目標 repo (owner/repo 路徑); 平台 / host 見 §`<repo>` 與 source / host. |
| `<version>` | ✅ | release 的 tag (如 `v1.2.3`); 不存在時依 `--commit` 建立, 見注意事項. |
| `--note STR` | ✅ (二擇一) | release note 內容. |
| `--note-file PATH` | ✅ (二擇一) | 從檔讀 release note (整個檔即 note 全文). |
| `--commit COMMIT` | ✅ (tag 不存在時) | 新 tag 指向的 commit: commit SHA, 或 `latest` (= 預設分支最新 commit). tag 已存在時不需要. |

## 示範

```
forgectl release create hello/hgsystem v1.2.3 --source github --note "first public release"

# tag 不存在: 以預設分支最新 commit 建 tag, note 從檔讀
forgectl release create hello/hgsystem v1.2.3 --source github --note-file CHANGELOG.md --commit latest

# 自架 GitLab (--host 指 base URL), 指定確切 commit
forgectl release create group/sub/proj v1.2.3 --source gitlab --host https://tech-git.nexcom.com.tw --note "patch release" --commit a1b2c3d
```

## 注意事項

- **目標 version 已是正式 release 時報錯** (非 0 退出). "已存在" **只認正式
  (published) release**; draft 是 `asset upload` 的暫存區, 不算已存在, 會被轉正.
- **note 來源**: `--note` (行內) 與 `--note-file` (從檔讀) **二擇一, 須提供且只能一個**;
  皆無或同時給 → 報錯. `--note-file` 讀整個檔作為 note 全文 (同 `--token-file`, 只接受
  實體檔, 無 stdin `-`).
- **tag 建立**: release 的 tag **即 `<version>`**. tag 已存在 → 沿用其 commit;
  不存在 → **必須以 `--commit` 明確指定**新 tag 指向的 commit, 未給即報錯 (不設隱含預設).
  `--commit` 可寫 commit SHA, 或特殊值 `latest` (= 預設分支最新 commit); tag 已存在時
  `--commit` 不需要 (給了則忽略).
- **不提供 draft / prerelease 旗標**: create 一律產生正式 release.
- 各平台行為 (見 §跨平台 release / asset 模型; 機制細節見 CLAUDE.md):
  - **GitHub**: 該 tag 已有正式 release → 報錯; 只有 `asset upload` 建立的 draft →
    **轉為正式** (publish) 並寫入 notes; 兩者皆無 → 新建.
  - **GitLab**: 該 tag 已有 Release → 報錯; 否則建立 Release, 並把該 version 的
    generic package files 以 asset link 連上.
  - (tag 不存在時如何建立, 見上方「tag 建立」.)

---

# asset upload

把一個以上本地檔上傳為某 version 的 asset. **release 不需要先存在.**

## 語法

```
forgectl asset upload <repo> <version> <path>[=NAME]...
```

全域旗標: `--source` (必填), 另有 `--host` / `--insecure` (見共用主題).

## 參數

| 參數 | 必填 | 說明 |
|---|---|---|
| `<repo>` | ✅ | 目標 repo (owner/repo 路徑); 平台 / host 見 §`<repo>` 與 source / host. |
| `<version>` | ✅ | 版本字串 (貫穿各指令的 join key; release 不需先存在). |
| `<path>[=NAME]...` | ✅ | 一個以上的本地**檔案** (不接受目錄); `=NAME` 指定上傳後的 asset 名, 省略則取 path 的 basename. |

## 示範

```
# 改名, Windows 路徑改名, 不改名, 三者混用
forgectl asset upload hello/hgsystem v1.2.3 --source github \
  dist/app-linux=app \
  D:\path\to\target.txt=beautiful_filename \
  dist/checksums.txt
```

## 注意事項

- `=NAME` 切分規則: 以**最後一個 `=`** 切; 若 `=` 之後那段含 `/` 或 `\` (看起來是
  路徑而非檔名), 則整項視為純路徑, 不改名 — 讓本身含 `=` 的路徑在常見情況下仍可用.
  `NAME` 須為平的檔名 (不含路徑分隔).
- 上傳**只讀取本地檔, 永遠不刪除或修改本地檔**.
- **一律需要具寫入權限的 token** (上傳即寫入操作); 認證見 §認證.
- Content-Type 依副檔名自動推斷, 無法判斷時用 `application/octet-stream`.
- 多檔行為: **逐一全部嘗試, 不中途中斷**; 結束時彙總成功與失敗清單, 只要有任一檔
  失敗即以非 0 退出.
- **同名既有 asset 一律覆蓋** — 沒有 `--replace` / `--keep-exist` 旗標, 上傳即 "以此
  為準". 各平台落點與覆蓋作法不同 (見 §跨平台 release / asset 模型; 機制細節見 CLAUDE.md):
  - **GitLab**: **永遠先刪同名舊 package file, 再上傳**新檔 (不依賴設定, 不留 orphan);
    刪除需較高權限, 不足 (403) 即報錯結束. release 已存在則建一個指向 by-name 下載
    URL 的 asset link (已存在則沿用).
  - **GitHub**: 該 tag 沒有 release 時自動建 draft release; 既有同名 asset 先刪再傳.

---

# asset download

下載某 release 的 asset, 可用 glob 選取.

## 語法

```
forgectl asset download <repo> <version> [pattern]... [-d DIR] [-o NAME] [--overwrite]
```

全域旗標: `--source` (必填), 另有 `--host` / `--insecure` (見共用主題).

## 參數

| 參數 | 必填 | 說明 |
|---|---|---|
| `<repo>` | ✅ | 目標 repo (owner/repo 路徑); 平台 / host 見 §`<repo>` 與 source / host. |
| `<version>` | ✅ | release 的 tag, 或特殊值 `latest` (最新正式 release, 排除 draft / prerelease). |
| `[pattern]...` | ⬜ | glob 比對 asset 名 (如 `'*linux*'`, `'*.sha256'`). 省略 = 全部; 多個取聯集; 無萬用字元 = 精確比對. |
| `-d, --dir DIR` | ⬜ | 下載放進 `DIR` (不存在則建立). 單檔, 多檔皆可. 預設為目前目錄. |
| `-o, --output NAME` | ⬜ | 指定輸出**檔名** (**僅單檔**; 多檔給 `-o` 即報錯). |
| `--overwrite` | ⬜ | 目標檔已存在時覆寫; 未給則存在即報錯. |

`-d` 與 `-o` 可**同時使用**: `-d` 決定目錄, `-o` 決定檔名, 合起來即 `DIR/NAME` (同
curl 的 `--output-dir` + `-o`). `-o` 省略則用 asset 原名; 兩者都不給 → 目前目錄, 原名.

## 示範

```
# 抓某版全部 asset 到目前目錄
forgectl asset download hello/hgsystem v1.2.3 --source github

# 只抓 linux 相關, 放進 ./dl/ (保留原名)
forgectl asset download hello/hgsystem v1.2.3 '*linux*' -d ./dl/ --source github

# 抓最新版的單一檔, 改存成別的名字
forgectl asset download hello/hgsystem latest checksums.txt -o sums.txt --source github

# 放進 ./dl/ 並改名 → ./dl/sums.txt
forgectl asset download hello/hgsystem latest checksums.txt -d ./dl/ -o sums.txt --source github
```

## 注意事項

- glob 的 `*` 等萬用字元**要加引號** (`'*linux*'`), 否則 shell 會先拿去比對本地檔.
- **無任何 asset 命中 → 不下載, 印出提示, 退出碼仍為 0** ("有就抓, 沒有就算了",
  方便寫腳本).
- 下載來源依平台不同 (端點細節見 CLAUDE.md):
  - **GitHub**: 走 release asset API 端點 (`Accept: application/octet-stream`),
    而非 `browser_download_url`.
  - **GitLab**: 列出 release 的 asset link, 比對名稱後, 從 link 指向的 by-name 下載
    URL 取檔.
- 私有 repo 需 token (見 §認證).
- 退出碼: 成功 (**含 "pattern 無命中"**) 為 0; release 不存在, 目標檔已存在又未給
  `--overwrite`, 認證失敗等才為非 0.

---

# 共用主題

## `<repo>` 與 source / host

`<repo>` 只寫 **owner/repo 路徑** (GitLab 可含子群組: `group/subgroup/project`); 平台
與 host 由旗標決定, **不需設定檔**:

- `--source github|gitlab` (**必填**): 指定平台, 不自動猜測.
- `--host URL` (選填): 自架站台的 base URL (如 `https://tech-git.nexcom.com.tw`);
  **省略即公開站** (github.com / gitlab.com).

```
hello/hgsystem    --source github                                          # 公開 GitHub
team/proj         --source gitlab                                          # 公開 GitLab
group/sub/proj    --source gitlab --host https://hello.there.com           # 自架 GitLab
```

## api_base 推導 (由 source + host)

不需 registry; api_base 直接由 `--source` 與 `--host` 依慣例算出:

| source | 自架 (`--host H`) | 公開 (無 `--host`) |
|---|---|---|
| gitlab | `H/api/v4` | `https://gitlab.com/api/v4` |
| github | `H/api/v3` | `https://api.github.com` |

GitHub **不對稱**: 公開站是 `api.github.com`, 自架 (GitHub Enterprise) 才是
`{host}/api/v3`; GitLab 則一律 `/api/v4`.

## 認證

token 是兩平台的共同基礎, 少數方式需搭一個 username. 認證以**固定位置的
credential 檔**為基底, 旗標只用來做欄位層級的覆寫; 沒有 `--credential` 旗標.
credential 檔的**格式, 搜尋位置, 欄位, 解析優先序, 安全性**見 [credential.md](credential.md).

旗標 (皆為覆寫對應欄位, 沒有環境變數層):

```
--token TOKEN          # 覆寫 token
--token-file PATH      # 覆寫 token, 從檔讀
--user NAME            # 覆寫 user
```

`--token-file` 的檔案**只放一個 token**, 不含其他欄位; 讀入時會修剪 (trim) 前後的空白,
控制字元與不可見字元 (如換行, 定位字元, BOM, 零寬空白), 僅留中間的 token 本體.

各平台 token 帶法對照:

| source | HTTP header | 需要 user |
|---|---|---|
| github | `Authorization: Bearer <token>` | 否 |
| gitlab | `PRIVATE-TOKEN: <token>` 或 `Bearer` | 否 (deploy token 才要) |

> 註: 2FA 與 "用 Google 登入" 不適用本工具. token 在建立時已完成 2FA;
> Google 登入屬 web OAuth, API 端一律使用 token.

## TLS 憑證驗證 (`--insecure`)

`--insecure`: **跳過 TLS 憑證驗證** (等同 curl `-k`), 供自架 host 使用自簽憑證的情況.

- **全域選項**, 各指令皆可用 (ping, release list / create, asset upload / download); 僅對
  https 連線有意義.
- 公開的 github.com / gitlab.com 有正式憑證, **不需也不應**使用.
- 會關閉**所有** TLS 驗證 (憑證鏈與主機名), 有中間人風險, 請只對信任的內網 host 使用;
  使用時印一行警告至 stderr.

## 跨平台 release / asset 模型 (摘要)

`<version>` 是貫穿各指令的 join key: `asset upload` **不需 release 先存在**,
`release create` 把該 version 正式發布. 兩平台 asset 儲存體不同:

- **GitLab**: asset = generic package (+ release asset link). `asset upload` 一律
  「先刪同名再上傳」; `release create` 建 Release 並把該 version 的 package files
  連成 asset link.
- **GitHub**: asset = release asset. `asset upload` 在無 release 時自動建 draft
  (暫存區) 並同名先刪再傳; `release create` 把 draft 轉正, 或新建.

> 詳細實作機制 (GitLab 反查、delete-then-upload 的 1:1 保證、commit 解析、tag 建立的
> `ref` / `target_commitish`、API 端點等) 見 **`CLAUDE.md`**.

## 設定檔 (草案)

只有**一個檔**, 即祕密 (token / user):

- `~/.config/forgectl/credential.{toml,yaml,yml,json}` — 祕密; 格式見 [credential.md](credential.md).

非祕密的平台 / host 設定改由 `--source` / `--host` 旗標提供, **不再有 config.toml**.
credential 檔仍以 **host 名為頂層鍵** (如 `["github.com"]`, `["tech-git.nexcom.com.tw"]`),
查詢時以實際連線的 host (公開站, 或 `--host` 指定者) 對應.
