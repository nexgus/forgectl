package forge

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// newHTTPClient builds the HTTP client the release / asset commands share. It
// honors --insecure the same way Ping does. It sets no overall Timeout: an
// asset upload or download may transfer a large file, and a fixed deadline
// would cut long but healthy transfers short.
func newHTTPClient(insecure bool) *http.Client {
	client := &http.Client{}
	if insecure {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}
	return client
}

// apiCaller issues authenticated requests against one platform's REST API. The
// authHeaders are attached to every request (the token plus any platform
// defaults, e.g. GitHub's Accept and API version); per-call headers override
// them. The platform types embed an apiCaller and add the endpoint logic.
type apiCaller struct {
	http        *http.Client
	authHeaders map[string]string
}

// do issues a request, merging the per-call headers over the auth headers. The
// caller closes the returned response body.
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

// req issues a request and returns the HTTP status and the fully read body. It
// returns an error only for transport failures; an HTTP error status is
// reported through the status code so callers can branch on 404, 403, and the
// like. It is for small JSON exchanges; downloads stream through getStream.
func (ac apiCaller) req(method, url string, headers map[string]string, body io.Reader) (int, []byte, error) {
	resp, err := ac.do(method, url, headers, body)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("reading response from %s: %w", url, err)
	}
	return resp.StatusCode, data, nil
}

// getJSON performs a GET and unmarshals a 2xx body into out (out may be nil to
// discard it). Any non-2xx status becomes an error.
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
			return fmt.Errorf("parsing response from %s: %w", url, err)
		}
	}
	return nil
}

// sendJSON sends a JSON-encoded payload and treats any non-2xx as an error.
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

// getStream streams a GET response body to w with the given extra headers,
// returning an error for any non-2xx status. It is the download path: the body
// is copied, never buffered whole.
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

// paginate fetches successive pages until a page returns fewer than perPage
// items (the last page). fetch decodes one page and returns its item count; it
// may return 0 to stop early, for example once a search has found its target.
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

// ok2xx reports whether status is a 2xx success code.
func ok2xx(status int) bool { return status >= 200 && status < 300 }

// statusError formats a non-2xx API response, including any human-readable
// message the JSON body carries.
func statusError(action string, status int, body []byte) error {
	if msg := apiMessage(body); msg != "" {
		return fmt.Errorf("%s: HTTP %d: %s", action, status, msg)
	}
	return fmt.Errorf("%s: HTTP %d", action, status)
}

// apiMessage extracts an error message from an API JSON body. GitHub uses a
// "message" field; GitLab uses "message" or "error".
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
