package flomo

import (
	"net/http"
	"time"
)

// This profile is captured from the Chromium browser used to run flomo Web.
// It lives entirely inside the volatile adapter so a browser or gateway change
// does not affect the CLI, domain, service, or storage contracts.
const (
	webOrigin        = "https://v.flomoapp.com"
	webReferer       = webOrigin + "/"
	webUserAgent     = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36"
	webClientHints   = `"Not=A?Brand";v="99", "Google Chrome";v="151", "Chromium";v="151"`
	memoSyncInterval = 500 * time.Millisecond
)

var gatewayRetryBackoff = [...]time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

// applyWebHeaders reproduces the browser-owned portion of flomo's CORS API
// request. The browser itself supplies these fields around Axios's headers.
func (c *Client) applyWebHeaders(request *http.Request, hasBody bool) {
	if hasBody {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept", "application/json, text/plain, */*")
	request.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	request.Header.Set("Origin", webOrigin)
	request.Header.Set("Referer", webReferer)
	request.Header.Set("User-Agent", webUserAgent)
	request.Header.Set("Sec-CH-UA", webClientHints)
	request.Header.Set("Sec-CH-UA-Mobile", "?0")
	request.Header.Set("Sec-CH-UA-Platform", `"macOS"`)
	request.Header.Set("Sec-Fetch-Dest", "empty")
	request.Header.Set("Sec-Fetch-Mode", "cors")
	request.Header.Set("Sec-Fetch-Site", "same-site")
	request.Header.Set("Priority", "u=1, i")
	request.Header.Set("platform", "Web")
	request.Header.Set("device-model", "Chrome")
	if c.deviceID != "" {
		request.Header.Set("device-id", c.deviceID)
	}
}
