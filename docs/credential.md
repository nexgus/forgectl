# forgectl credential 檔格式

定義 forgectl 讀取認證資訊 (token 與選用的 username) 的檔案格式.
完整認證流程見 [cli.md](cli.md) 的 "認證" 一節, 本文聚焦在**檔案本身的格式**.

## 概述

credential 檔放在**固定位置**, 使用者寫了就讀; 旗標 (`--token` / `--token-file` /
`--user`) 只用來覆寫檔內對應欄位.

**搜尋位置** (依序, 存在即用; `$XDG_CONFIG_HOME` 優先於 `~/.config`; Windows 則為
作業系統設定目錄, 如 `%AppData%\forgectl\`):

```
~/.config/forgectl/credential.toml
~/.config/forgectl/credential.yaml
~/.config/forgectl/credential.yml
~/.config/forgectl/credential.json
```

- 格式**依副檔名判斷** (`.toml` / `.yaml` / `.yml` / `.json`).
- **同一目錄只允許存在一個 `credential.*` 檔**; 若同時有多個即**報錯**, 要求保留其一
  (避免 "以為在用 A 檔, 實際用了 B 檔" 的隱性優先序).
- 三種格式**擇一**即可, 內容對等.

一個 credential 檔**整檔擇一**為 "扁平" 或 "階層", 兩者不混用:

- **扁平** — 頂層只有 `token` / `user`, 這組認證**套用到所有 host**. 適用於所有 host 共用
  同一把 token (與選用的 `user`) 的情況; 雖少見, 但寫法最精簡.
- **階層** — 頂層直接以 **host 為鍵** (如 `github.com`, `gitlab.mycorp.com`), 各 host 各自
  帶 `token` / `user`. 用 host 而非 source, 才能區分 `gitlab.com` 與自架的
  `gitlab.mycorp.com` (即 `--host` 指定的 host; 省略 `--host` 時為公開站 github.com / gitlab.com).

判定方式: 頂層出現 `token` / `user` 即為扁平, 頂層為 host 鍵即為階層; 兩者**混寫即報錯**
(同 "只允許一個 credential 檔" 的精神, 不做隱性猜測). 以下兩節各列出 TOML / YAML / JSON
三種寫法; JSON 不支援註解.

## 扁平 (全域單一認證)

所有 host 共用同一組認證時用這種; 整個檔案就是一組 `token` 與選用的 `user`.

### TOML

```toml
token = "ghp_default"
user  = "nexgus"
```

### YAML

```yaml
token: ghp_default
user: nexgus
```

### JSON

```json
{
  "token": "ghp_default",
  "user": "nexgus"
}
```

## 階層 (分 host 設定)

各 host 各自設定; host 名稱直接作為頂層的鍵, 不需 `hosts` 外層. 各 host 的 `user` 仍為
選用 (下例 `github.com` 只給 token).

### TOML

```toml
["github.com"]
token = "ghp_github"

["gitlab.mycorp.com"]
token = "glpat_xxx"
user  = "deploy"
```

### YAML

```yaml
github.com:
  token: ghp_github
gitlab.mycorp.com:
  token: glpat_xxx
  user: deploy
```

### JSON

```json
{
  "github.com": { "token": "ghp_github" },
  "gitlab.mycorp.com": { "token": "glpat_xxx", "user": "deploy" }
}
```

## 欄位

兩種寫法都只用到 `token` 與 `user`, 語意相同; `user` 一律**選用** (少數 basic-auth 方式
才需要, 如 GitLab deploy token).

**扁平** — 頂層兩個欄位:

| 欄位 | 型別 | 說明 |
|---|---|---|
| `token` | string | 套用到所有 host 的 token. |
| `user` | string | 套用到所有 host 的 user (選用). |

**階層** — 每個頂層鍵是一個 host, 值為該 host 的設定:

| 欄位 | 型別 | 說明 |
|---|---|---|
| `<host>` | map | host 名稱 (如 `github.com`) 作為鍵. |
| `<host>.token` | string | 該 host 的 token. |
| `<host>.user` | string | 該 host 的 user (選用). |

**最小範例** (扁平; 一把 token 套用所有 host):

```toml
token = "ghp_xxxxx"
```

## 解析優先序

針對目標 host `H`, **旗標永遠勝過檔案**; credential 檔依其為扁平或階層, 各取一個值.

token (取第一個非空):

```
1. --token (行內旗標)
2. --token-file (旗標, 從檔讀)
3. credential 檔:
     扁平 — 頂層 token
     階層 — host "H" 的 token (未列出 H 則略過)
4. 無
```

user (取第一個非空):

```
1. --user (行內旗標)
2. credential 檔:
     扁平 — 頂層 user
     階層 — host "H" 的 user (未列出 H 則略過)
3. 無
```

credential 檔不存在, 旗標也都沒給時, **不帶認證** — 不會跳互動 prompt.
公開 repo 的 `release list` / `asset download` 在不帶認證下仍可運作 (僅 rate limit 較低).

## 與其他設定的關係

- credential 檔只放**祕密** (token / user). 非祕密的平台 / host 改由 CLI 旗標
  `--source` / `--host` 提供, **沒有 config.toml**; credential 檔是唯一的設定檔.
- 旗標 `--token` / `--token-file` / `--user` 是**欄位層級覆寫**, 疊在 credential 檔之上;
  沒有 `--credential` 旗標 (檔案一律從固定位置讀).

## 安全性

- credential 檔含祕密, 建議權限設為 **`0600`**; 若檔案 world-readable, CLI 應提出警告.
- **不要納入版本控制**; 建議加進 `.gitignore`.
- 盡量別用 `--token` 把 token 直接打在命令列 (會進 shell history 與 `ps`);
  優先用 credential 檔, CI 場景用 `--token-file`.
