package forge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// auth 保存一次連線所需的認證資訊, 以及各值來源的人類可讀描述.
// ping 只印出來源 (不印 token 本體), 讓使用者確認哪個設定生效.
type auth struct {
	Token       string
	User        string
	TokenSource string // Token 的來源, 例如 "--token 旗標"
	UserSource  string // User 的來源
	Warnings    []string
}

// resolveAuth 依 docs/credential.md 的優先序, 解析指定 host 的 token 與 user:
// 旗標覆蓋 credential file; credential file 從固定位置讀取 (無旗標可指定路徑).
// 只有設定格式有誤 (credential file 不明確、token file 無法讀取) 才回傳 error;
// 解析結果為空 (匿名使用) 是合法狀態.
func resolveAuth(cfg Config, host string) (auth, error) {
	var a auth

	file, fileToken, fileUser, err := readCredential(host)
	if err != nil {
		return auth{}, err
	}
	if file != "" {
		if w := worldReadableWarning(file); w != "" {
			a.Warnings = append(a.Warnings, w)
		}
	}

	// token 優先序: --token > --token-file > credential file > none.
	switch {
	case cfg.Token != "":
		a.Token = cfg.Token
		a.TokenSource = "--token 旗標"
	case cfg.TokenFile != "":
		tok, err := readTokenFile(cfg.TokenFile)
		if err != nil {
			return auth{}, err
		}
		a.Token = tok
		a.TokenSource = fmt.Sprintf("--token-file (%s)", cfg.TokenFile)
	case fileToken != "":
		a.Token = fileToken
		a.TokenSource = fmt.Sprintf("credential file (%s)", file)
	default:
		a.TokenSource = "none"
	}

	// user 優先序: --user > credential file > none.
	switch {
	case cfg.User != "":
		a.User = cfg.User
		a.UserSource = "--user 旗標"
	case fileUser != "":
		a.User = fileUser
		a.UserSource = fmt.Sprintf("credential file (%s)", file)
	default:
		a.UserSource = "none"
	}

	return a, nil
}

// credentialDirs 依 docs/credential.md 的規定, 按優先序回傳搜尋 credential file
// 的目錄清單: 非 Windows 時 $XDG_CONFIG_HOME 優先於 ~/.config;
// Windows 則使用 %AppData%.
func credentialDirs() []string {
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("AppData"); appData != "" {
			return []string{filepath.Join(appData, "forgectl")}
		}
		if dir, err := os.UserConfigDir(); err == nil {
			return []string{filepath.Join(dir, "forgectl")}
		}
		return nil
	}
	var dirs []string
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		dirs = append(dirs, filepath.Join(xdg, "forgectl"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".config", "forgectl"))
	}
	return dirs
}

// credentialExts 列出可識別的 credential file 副檔名; 解析器依副檔名選擇.
var credentialExts = []string{"toml", "yaml", "yml", "json"}

// findCredentialFile 在搜尋目錄中找出唯一的 credential.* 檔並回傳路徑;
// 若不存在則回傳 "". 若同一目錄內有多個 credential.* 檔則回傳 error —
// docs/credential.md 禁止此情況, 不猜測優先序.
func findCredentialFile() (string, error) {
	for _, dir := range credentialDirs() {
		var found []string
		for _, ext := range credentialExts {
			p := filepath.Join(dir, "credential."+ext)
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				found = append(found, p)
			}
		}
		switch len(found) {
		case 0:
			continue
		case 1:
			return found[0], nil
		default:
			return "", fmt.Errorf("%s 目錄下有多個 credential file (%s), 請保留其中一個", dir, strings.Join(baseNames(found), ", "))
		}
	}
	return "", nil
}

// readCredential 找到並解析 credential file, 回傳檔案路徑以及適用於指定 host
// 的 token 與 user. 檔案不存在不視為 error (回傳空值); 格式有誤才回傳 error.
func readCredential(host string) (path, token, user string, err error) {
	path, err = findCredentialFile()
	if err != nil {
		return "", "", "", err
	}
	if path == "" {
		return "", "", "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", "", fmt.Errorf("讀取 credential file %s 失敗: %w", path, err)
	}
	ext := strings.TrimPrefix(filepath.Ext(path), ".")
	token, user, err = parseCredential(data, ext, host)
	if err != nil {
		return "", "", "", fmt.Errorf("credential file %s: %w", path, err)
	}
	return path, token, user, nil
}

// parseCredential 依副檔名解析 credential 位元組, 回傳對應 host 的 token 與 user.
// 檔案格式分為扁平 (頂層 token/user 適用所有 host) 與階層 (host 名稱為頂層 key)
// 兩種; 混用兩種格式為 error, host 項目非 table 亦為 error.
func parseCredential(data []byte, ext, host string) (token, user string, err error) {
	var raw map[string]any
	switch ext {
	case "toml":
		if err := toml.Unmarshal(data, &raw); err != nil {
			return "", "", err
		}
	case "yaml", "yml":
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return "", "", err
		}
	case "json":
		if err := json.Unmarshal(data, &raw); err != nil {
			return "", "", err
		}
	default:
		return "", "", fmt.Errorf("不支援的 credential 格式 %q", ext)
	}

	flat, hier := classify(raw)
	switch {
	case flat && hier:
		return "", "", fmt.Errorf("credential file 同時包含扁平格式 (頂層 token/user) 與階層格式 (per-host), 請擇一使用")
	case flat:
		t, _ := stringField(raw, "token")
		u, _ := stringField(raw, "user")
		return t, u, nil
	case hier:
		entry, ok := raw[host]
		if !ok {
			return "", "", nil // host 未列出, 無對應 credential
		}
		m, ok := entry.(map[string]any)
		if !ok {
			return "", "", fmt.Errorf("host %q 的項目不是 table", host)
		}
		t, _ := stringField(m, "token")
		u, _ := stringField(m, "user")
		return t, u, nil
	default:
		return "", "", nil // 空檔案
	}
}

// classify 判斷已解析的 credential map 是否為扁平格式 (含頂層 token/user)
// 及是否為階層格式 (含任何 host table). 格式正確的檔案恰為其中之一.
func classify(raw map[string]any) (flat, hier bool) {
	for k, v := range raw {
		if _, isMap := v.(map[string]any); isMap {
			hier = true
			continue
		}
		if k == "token" || k == "user" {
			flat = true
		}
	}
	return flat, hier
}

// stringField 回傳 m 中 key 對應的字串值 (若存在且為字串型別).
func stringField(m map[string]any, key string) (string, bool) {
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// baseNames 回傳每個路徑的基本檔名.
func baseNames(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = filepath.Base(p)
	}
	return out
}

// readTokenFile 從檔案讀取 token, 依 docs/cli.md 的規定去除前後的空白、
// 控制字元與不可見字元 (換行、tab、BOM、零寬空格), 只保留 token 本體.
func readTokenFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("讀取 --token-file %s 失敗: %w", path, err)
	}
	tok := strings.TrimFunc(string(data), func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || isInvisible(r)
	})
	if tok == "" {
		return "", fmt.Errorf("--token-file %s 未包含任何 token", path)
	}
	return tok, nil
}

// isInvisible 判斷 r 是否為零寬 / BOM code point, 這類字元應從 token 中去除,
// 但不在 unicode.IsSpace 或 unicode.IsControl 的範圍內.
// 以 code point 比對, 避免原始碼內出現字面不可見字元.
func isInvisible(r rune) bool {
	switch r {
	case 0xFEFF, // BOM / zero-width no-break space
		0x200B, // zero-width space
		0x200C, // zero-width non-joiner
		0x200D, // zero-width joiner
		0x2060: // word joiner
		return true
	}
	return false
}

// worldReadableWarning 當 credential file 可被群組或其他使用者讀取時回傳警告
// (建議權限為 0600). 在 Windows 上為 no-op, 因其權限模型不同.
func worldReadableWarning(path string) string {
	if runtime.GOOS == "windows" {
		return ""
	}
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Sprintf("credential file %s 可被其他使用者存取 (mode %#o), 建議執行 chmod 600", path, perm)
	}
	return ""
}
