package flomo

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"github.com/songquanpeng/flomo-cli/internal/domain"
)

func TestLoginWebAPIContract(t *testing.T) {
	fixedTime := time.Unix(1_700_000_000, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/user/login_by_email" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q", r.Method)
		}
		assertWebHeaders(t, r)
		var body map[string]any
		decoder := json.NewDecoder(r.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&body); err != nil {
			t.Fatal(err)
		}
		gotSign, _ := body["sign"].(string)
		delete(body, "sign")
		if wantSign := sign(body); gotSign != wantSign {
			t.Errorf("sign = %q, want %q", gotSign, wantSign)
		}
		if body["email"] != "agent@example.com" || body["password"] != "secret" {
			t.Errorf("credentials not mapped: %#v", body)
		}
		if body["api_key"] != "flomo_web" || body["app_version"] != "4.0" || body["platform"] != "web" {
			t.Errorf("web protocol metadata missing: %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"access_token":"token","nickname":"Agent"}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "", "device", WithClock(func() time.Time { return fixedTime }))
	result, err := client.Login(context.Background(), "agent@example.com", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if result.AccessToken != "token" || result.Nickname != "Agent" {
		t.Fatalf("result = %#v", result)
	}
}

func TestMemoAdapterMapsV1SyncAndV2Write(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertWebHeaders(t, r)
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/memo/updated/":
			if r.URL.Query().Get("latest_updated_at") != "42" || r.URL.Query().Get("tz") != "8:0" {
				t.Errorf("sync query = %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"code":0,"data":[{"slug":"m1","content":"<p>hello</p>","created_at":"2026-01-01 00:00:00","updated_at":"2026-01-02 00:00:00"}]}`))
		case "/api/v2/memo":
			if r.Method != http.MethodPost {
				t.Errorf("write method = %s", r.Method)
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"slug":"m2","content":"<p>new</p>"}}`))
		case "/api/v2/memo/m1":
			if r.Method != http.MethodPut {
				t.Errorf("edit method = %s", r.Method)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["content"] != "<p>edited</p>" || body["created_at"] != "2026-01-01 00:00:00" {
				t.Errorf("edit body = %#v", body)
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"slug":"m1","content":"<p>edited</p>"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "token", "device")
	memos, err := client.UpdatedMemos(context.Background(), 200, 42, "m0", "8:0")
	if err != nil {
		t.Fatal(err)
	}
	if len(memos) != 1 || memos[0].Slug != "m1" {
		t.Fatalf("memos = %#v", memos)
	}
	created, err := client.AddMemo(context.Background(), domain.AddMemoRequest{Content: "<p>new</p>", CreatedAt: 1, MemoFrom: "api", Source: "flomo_cli", Timezone: "8:0"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Slug != "m2" {
		t.Fatalf("created = %#v", created)
	}
	edited, err := client.EditMemo(context.Background(), "m1", domain.EditMemoRequest{
		Content: "<p>edited</p>", CreatedAt: "2026-01-01 00:00:00", LocalUpdatedAt: 2,
		MemoFrom: "api", Source: "flomo_cli", FileIDs: []any{1}, Timezone: "8:0", Pinned: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if edited.Slug != "m1" || edited.Content != "<p>edited</p>" {
		t.Fatalf("edited = %#v", edited)
	}
}

func TestMemoSyncMatchesBrowserPacing(t *testing.T) {
	current := time.Unix(1_700_000_000, 0)
	var sleeps []time.Duration
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"data":[]}`))
	}))
	defer server.Close()
	client := NewClient(server.URL, "token", "device",
		WithClock(func() time.Time { return current }),
		func(client *Client) {
			client.sleep = func(_ context.Context, duration time.Duration) error {
				sleeps = append(sleeps, duration)
				current = current.Add(duration)
				return nil
			}
		},
	)
	for range 2 {
		if _, err := client.UpdatedMemos(context.Background(), 200, 0, "", "8:0"); err != nil {
			t.Fatal(err)
		}
	}
	if want := []time.Duration{memoSyncInterval}; !reflect.DeepEqual(sleeps, want) {
		t.Fatalf("sync sleeps = %v, want %v", sleeps, want)
	}
}

func TestRetriesTransientGateway403OnlyForIdempotentRequests(t *testing.T) {
	getAttempts := 0
	postAttempts := 0
	var sleeps []time.Duration
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			getAttempts++
			if getAttempts == 1 {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`<html><h1>403 Forbidden</h1><center>nginx</center></html>`))
				return
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{}}`))
			return
		}
		postAttempts++
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<html><h1>403 Forbidden</h1><center>nginx</center></html>`))
	}))
	defer server.Close()
	client := NewClient(server.URL, "token", "device", func(client *Client) {
		client.sleep = func(_ context.Context, duration time.Duration) error {
			sleeps = append(sleeps, duration)
			return nil
		}
	})
	if _, err := client.Me(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Login(context.Background(), "agent@example.com", "wrong"); err == nil {
		t.Fatal("login unexpectedly succeeded")
	}
	if getAttempts != 2 || postAttempts != 1 {
		t.Fatalf("attempts: GET=%d POST=%d, want GET=2 POST=1", getAttempts, postAttempts)
	}
	if want := []time.Duration{time.Second}; !reflect.DeepEqual(sleeps, want) {
		t.Fatalf("retry sleeps = %v, want %v", sleeps, want)
	}
}

func TestDecodesAllBrowserAdvertisedContentEncodings(t *testing.T) {
	payload := []byte(`{"code":0,"data":[]}`)
	encoders := map[string]func(*testing.T, []byte) []byte{
		"gzip": func(t *testing.T, input []byte) []byte {
			var output bytes.Buffer
			writer := gzip.NewWriter(&output)
			if _, err := writer.Write(input); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			return output.Bytes()
		},
		"deflate": func(t *testing.T, input []byte) []byte {
			var output bytes.Buffer
			writer := zlib.NewWriter(&output)
			if _, err := writer.Write(input); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			return output.Bytes()
		},
		"br": func(t *testing.T, input []byte) []byte {
			var output bytes.Buffer
			writer := brotli.NewWriter(&output)
			if _, err := writer.Write(input); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			return output.Bytes()
		},
		"zstd": func(t *testing.T, input []byte) []byte {
			writer, err := zstd.NewWriter(nil)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			return writer.EncodeAll(input, nil)
		},
	}
	for encoding, encode := range encoders {
		t.Run(encoding, func(t *testing.T) {
			response := &http.Response{
				Header: http.Header{"Content-Encoding": []string{encoding}},
				Body:   io.NopCloser(bytes.NewReader(encode(t, payload))),
			}
			got, err := readResponseBody(response)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, payload) {
				t.Fatalf("decoded body = %q, want %q", got, payload)
			}
		})
	}
}

func assertWebHeaders(t *testing.T, request *http.Request) {
	t.Helper()
	want := map[string]string{
		"Accept":             "application/json, text/plain, */*",
		"Accept-Encoding":    "gzip, deflate, br, zstd",
		"Accept-Language":    "zh-CN,zh;q=0.9,en;q=0.8",
		"Origin":             webOrigin,
		"Referer":            webReferer,
		"User-Agent":         webUserAgent,
		"Sec-CH-UA":          webClientHints,
		"Sec-CH-UA-Mobile":   "?0",
		"Sec-CH-UA-Platform": `"macOS"`,
		"Sec-Fetch-Dest":     "empty",
		"Sec-Fetch-Mode":     "cors",
		"Sec-Fetch-Site":     "same-site",
		"Priority":           "u=1, i",
		"platform":           "Web",
		"device-model":       "Chrome",
	}
	for header, expected := range want {
		if got := request.Header.Get(header); got != expected {
			t.Errorf("%s = %q, want %q", header, got, expected)
		}
	}
}
