package forge

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
)

// newHTTPClient 建立 release / asset 指令共用的 HTTP client. 處理 --insecure
// 的方式與 Ping 相同. 不設整體 Timeout: asset 上傳或下載可能傳輸大型檔案,
// 固定截止時間會中斷長時間但正常的傳輸.
func newHTTPClient(insecure bool) *http.Client {
	client := &http.Client{}
	if insecure {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}
	return client
}

// apiCaller 對單一平台的 REST API 發出已認證的請求. authHeaders 會附加到每個
// 請求 (token 加上任何平台預設值, 例如 GitHub 的 Accept 與 API 版本);
// 逐次呼叫的 header 可覆寫它們. 平台型別內嵌 apiCaller 並加入 endpoint 邏輯.
type apiCaller struct {
	http        *http.Client
	authHeaders map[string]string
}

// do 發出請求, 將逐次呼叫的 header 合併覆寫到認證 header 上.
// 呼叫方負責關閉回傳的 response body.
func (ac apiCaller) do(method, url string, headers map[string]string, body io.Reader) (*http.Response, error) {
	merged := make(map[string]string, len(ac.authHeaders)+len(headers))
	for k, v := range ac.authHeaders {
		merged[k] = v
	}
	for k, v := range headers {
		merged[k] = v
	}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	for k, v := range merged {
		req.Header.Set(k, v)
	}
	return ac.http.Do(req)
}

// req 發出請求並回傳 HTTP 狀態碼與完整讀取的 body. 僅在傳輸層失敗時回傳 error;
// HTTP 錯誤狀態透過狀態碼回報, 讓呼叫方可依 404, 403 等情況分支處理.
// 適用於小型 JSON 交換; 下載則透過 getStream 串流處理.
func (ac apiCaller) req(method, url string, headers map[string]string, body io.Reader) (int, []byte, error) {
	resp, err := ac.do(method, url, headers, body)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("讀取 %s 的回應時發生錯誤: %w", url, err)
	}
	return resp.StatusCode, data, nil
}

// getJSON 執行 GET 並將 2xx body 反序列化至 out (out 可為 nil 以捨棄內容).
// 任何非 2xx 狀態均視為 error.
func (ac apiCaller) getJSON(url string, out any) error {
	status, body, err := ac.req("GET", url, nil, nil)
	if err != nil {
		return err
	}
	if !ok2xx(status) {
		return statusError("GET "+url, status, body)
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("解析 %s 的回應時發生錯誤: %w", url, err)
		}
	}
	return nil
}

// sendJSON 傳送 JSON 編碼的 payload, 並將任何非 2xx 狀態視為 error.
func (ac apiCaller) sendJSON(method, url string, payload any, action string) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	status, body, err := ac.req(method, url, map[string]string{"Content-Type": "application/json"}, bytes.NewReader(data))
	if err != nil {
		return err
	}
	if !ok2xx(status) {
		return statusError(action, status, body)
	}
	return nil
}

// getStream 以指定的額外 header 將 GET response body 串流寫入 w,
// 任何非 2xx 狀態均回傳 error. 此為下載路徑: body 逐段複製, 不整體緩衝.
func (ac apiCaller) getStream(url string, headers map[string]string, w io.Writer) error {
	resp, err := ac.do("GET", url, headers, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if !ok2xx(resp.StatusCode) {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return statusError("GET "+url, resp.StatusCode, body)
	}
	// 2xx 但最終落在登入頁: 認證失敗 (token 缺失/失效或權限不足) 時, 伺服器常把下載
	// 請求重導至登入頁再回 200 HTML, 否則整頁登入 HTML 會被當成 asset 寫進檔案.
	// asset 可為任意格式 (含 HTML), 故不以內容格式判定; 而是看「跟隨重導後是否落在
	// 登入頁」這個與 asset 內容無關的特徵 — 正常下載端點不會從登入頁供檔.
	if isLoginPage(resp.Request.URL.Path) && looksLikeHTML(resp.Header.Get("Content-Type")) {
		return fmt.Errorf("下載被重導至登入頁 (%s); 多為認證失敗: token 缺失/失效或權限不足", resp.Request.URL)
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

// isLoginPage 依路徑判斷是否為平台的登入頁: GitLab 為 /users/sign_in,
// GitHub 為 /login 或 /session (含自架站於子路徑下的情形).
func isLoginPage(path string) bool {
	p := strings.ToLower(path)
	return strings.Contains(p, "/users/sign_in") ||
		strings.HasSuffix(p, "/login") ||
		strings.HasSuffix(p, "/session")
}

// looksLikeHTML 回報 Content-Type 是否為 HTML 頁面. 僅用來與 isLoginPage 搭配,
// 確認登入頁回應; 不單獨作為「不是 asset」的判據 (合法 asset 可以是 HTML).
func looksLikeHTML(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return mt == "text/html" || mt == "application/xhtml+xml"
}

// paginate 依序取得各頁, 直到某頁回傳的項目數少於 perPage (即最後一頁).
// fetch 解碼單頁並回傳其項目數; 可回傳 0 提前停止,
// 例如搜尋已找到目標時.
func paginate(perPage int, fetch func(page int) (int, error)) error {
	for page := 1; ; page++ {
		n, err := fetch(page)
		if err != nil {
			return err
		}
		if n < perPage {
			return nil
		}
	}
}

// ok2xx 回報 status 是否為 2xx 成功狀態碼.
func ok2xx(status int) bool { return status >= 200 && status < 300 }

// statusError 格式化非 2xx 的 API 回應, 包含 JSON body 中任何可供閱讀的訊息.
func statusError(action string, status int, body []byte) error {
	if msg := apiMessage(body); msg != "" {
		return fmt.Errorf("%s: HTTP %d: %s", action, status, msg)
	}
	return fmt.Errorf("%s: HTTP %d", action, status)
}

// apiMessage 從 API JSON body 中提取錯誤訊息. GitHub 使用 "message" 欄位;
// GitLab 使用 "message" 或 "error".
func apiMessage(body []byte) string {
	var m struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(body, &m) == nil {
		if m.Message != "" {
			return m.Message
		}
		if m.Error != "" {
			return m.Error
		}
	}
	return ""
}
