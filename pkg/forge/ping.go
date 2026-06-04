package forge

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// pingTimeout 限制 ping 發出的每個 HTTP 請求的逾時時間.
const pingTimeout = 30 * time.Second

// Ping 驗證遠端設定. 解析連線目標與 credential 後, 執行分層檢查:
//
//  1. 連線: 發出不需 token 的請求, 以驗證 --host, --insecure 及 TLS,
//     即使沒有 credential 也能執行. 只要收到任何 HTTP 回應 (即使是錯誤狀態碼)
//     即視為連線成功; 只有傳輸層錯誤才算連線失敗.
//  2. 認證: 若已解析到 token, 呼叫平台的目前使用者 endpoint, 確認 token 有效
//     並回報已認證的身分. 無 token 時略過此層 (匿名為合法用法).
//
// 已解析的設定與每層結果會印至 stdout; 任何一層失敗時 Ping 回傳 error (非零結束碼).
func (c *Client) Ping() error {
	base, err := apiBase(c.cfg.Source, c.cfg.Host)
	if err != nil {
		return err
	}
	host, err := credentialHost(c.cfg.Source, c.cfg.Host)
	if err != nil {
		return err
	}
	a, err := resolveAuth(c.cfg, host)
	if err != nil {
		return err
	}

	emitWarnings(a, c.cfg.Insecure)

	c.printSettings(base, host, a)

	client := &http.Client{Timeout: pingTimeout}
	if c.cfg.Insecure {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}

	// 第一層: 連線.
	connURL := base + connectivityPath(c.cfg.Source)
	resp, err := doGet(client, connURL, nil)
	if err != nil {
		return fmt.Errorf("連線檢查失敗 (%s): %w", connURL, err)
	}
	resp.Body.Close()
	fmt.Println("Connectivity: OK")

	// 第二層: 認證.
	if a.Token == "" {
		fmt.Println("Auth:         skipped (no credentials resolved)")
		return nil
	}
	identity, err := c.checkAuth(client, base, a.Token)
	if err != nil {
		return err
	}
	fmt.Printf("Auth:         OK - authenticated as %s\n", identity)
	return nil
}

// printSettings 將已解析的連線設定寫至 stdout. 只印 token 來源, 不印 token 本體.
func (c *Client) printSettings(base, host string, a auth) {
	site := "public"
	if c.cfg.Host != "" {
		site = "self-hosted"
	}
	tlsVerify := "on"
	if c.cfg.Insecure {
		tlsVerify = "off (--insecure)"
	}
	line := func(label, value string) { fmt.Printf("%-14s%s\n", label, value) }
	line("Source:", c.cfg.Source)
	line("Host:", fmt.Sprintf("%s (%s)", host, site))
	line("API base:", base)
	line("TLS verify:", tlsVerify)
	line("Token:", a.TokenSource)
	if a.User != "" {
		line("User:", fmt.Sprintf("%s (%s)", a.User, a.UserSource))
	} else {
		line("User:", a.UserSource)
	}
	fmt.Println()
}

// connectivityPath 回傳附加在 API base 後的連線探測路徑: 各平台上不需認證的 endpoint.
func connectivityPath(source string) string {
	switch source {
	case "gitlab":
		return "/version"
	default: // github: API root 不需認證即可回應
		return ""
	}
}

// checkAuth 以 token 呼叫平台的目前使用者 endpoint, 成功時回傳可讀的身分字串.
// 非 2xx 回應代表 credential 不適用於此 endpoint; 在 GitLab 上包括 deploy token,
// 因為 deploy token 無法存取 /user.
func (c *Client) checkAuth(client *http.Client, base, token string) (string, error) {
	url := base + "/user"
	var headers map[string]string
	switch c.cfg.Source {
	case "github":
		headers = map[string]string{
			"Authorization":        "Bearer " + token,
			"Accept":               "application/vnd.github+json",
			"X-GitHub-Api-Version": "2022-11-28",
		}
	case "gitlab":
		headers = map[string]string{"PRIVATE-TOKEN": token}
	}

	resp, err := doGet(client, url, headers)
	if err != nil {
		return "", fmt.Errorf("認證檢查失敗 (%s): %w", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", c.authError(resp.StatusCode)
	}
	return parseIdentity(c.cfg.Source, body)
}

// authError 將目前使用者 endpoint 的非 2xx 狀態碼轉換為可採取行動的 error.
func (c *Client) authError(status int) error {
	switch status {
	case http.StatusUnauthorized:
		if c.cfg.Source == "gitlab" {
			return fmt.Errorf("認證失敗: 401 Unauthorized; token 無效或已過期, 或為 deploy token (deploy token 無法存取 /user, 請改以實際 asset 操作驗證)")
		}
		return fmt.Errorf("認證失敗: 401 Unauthorized; token 無效或已過期")
	case http.StatusForbidden:
		return fmt.Errorf("認證失敗: 403 Forbidden; token 缺少所需權限範圍, 或請求遭到速率限制")
	default:
		return fmt.Errorf("認證失敗: /user 回傳非預期狀態碼 %d", status)
	}
}

// parseIdentity 從目前使用者 endpoint 的回應主體中擷取已認證的身分:
// GitHub 使用 login 欄位, GitLab 使用 username 欄位.
func parseIdentity(source string, body []byte) (string, error) {
	switch source {
	case "github":
		var u struct {
			Login string `json:"login"`
			ID    int64  `json:"id"`
		}
		if err := json.Unmarshal(body, &u); err != nil || u.Login == "" {
			return "", fmt.Errorf("認證成功, 但無法解析使用者回應")
		}
		return fmt.Sprintf("%q (id %d)", u.Login, u.ID), nil
	case "gitlab":
		var u struct {
			Username string `json:"username"`
			ID       int64  `json:"id"`
		}
		if err := json.Unmarshal(body, &u); err != nil || u.Username == "" {
			return "", fmt.Errorf("認證成功, 但無法解析使用者回應")
		}
		return fmt.Sprintf("%q (id %d)", u.Username, u.ID), nil
	default:
		return "", fmt.Errorf("unknown source %q", source)
	}
}

// doGet 以指定的 headers 發出 GET 請求並回傳回應, 呼叫端須自行關閉回應主體.
func doGet(client *http.Client, url string, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return client.Do(req)
}
