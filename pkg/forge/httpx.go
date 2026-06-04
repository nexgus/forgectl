package forge

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	_, err = io.Copy(w, resp.Body)
	return err
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
