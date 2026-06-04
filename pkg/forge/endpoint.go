package forge

import (
	"fmt"
	"net/url"
	"strings"
)

// apiBase 依 source 與可選的自架 host 推導 REST API base URL,
// 對應規則見 docs/cli.md 的對照表:
//
//	source  自架 (--host H)           公開站 (無 --host)
//	gitlab  H/api/v4                 https://gitlab.com/api/v4
//	github  H/api/v3                 https://api.github.com
//
// GitHub 不對稱: 公開站為 api.github.com, 自架 GitHub Enterprise
// 則在 {host}/api/v3 下提供 API.
func apiBase(source, host string) (string, error) {
	host = strings.TrimRight(strings.TrimSpace(host), "/")
	switch source {
	case "gitlab":
		if host == "" {
			return "https://gitlab.com/api/v4", nil
		}
		return host + "/api/v4", nil
	case "github":
		if host == "" {
			return "https://api.github.com", nil
		}
		return host + "/api/v3", nil
	default:
		return "", fmt.Errorf("不支援的 source %q", source)
	}
}

// credentialHost 回傳此連線的 credential 檔索引鍵所對應的 host 名稱:
// 省略 --host 時取公開站的 host, 否則取 --host 的 host 部分.
// 依 docs/credential.md, credential 檔以 host 名稱為鍵
// (例如 "github.com" 或 "gitlab.mycorp.com").
func credentialHost(source, host string) (string, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		switch source {
		case "gitlab":
			return "gitlab.com", nil
		case "github":
			return "github.com", nil
		default:
			return "", fmt.Errorf("不支援的 source %q", source)
		}
	}
	// --host 可以是完整 base URL (如 https://gitlab.mycorp.com),
	// 也可以是不含 scheme 的裸 host; 兩種情況都只取 host 名稱.
	raw := host
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("無效的 --host %q", host)
	}
	return u.Hostname(), nil
}
