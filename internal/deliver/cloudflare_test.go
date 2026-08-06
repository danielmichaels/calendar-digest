package deliver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCloudflareSendsJSONWithBearerToken(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/client/v4/accounts/account-123/email/sending/send" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token-123" {
			t.Errorf("Authorization = %q", got)
		}
		var got cloudflareEmailRequest
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if got.To != "ada@example.com" || got.From != "digest@example.com" || got.Subject != "subject" || got.Text != "text" || got.HTML != "<p>html</p>" {
			t.Errorf("request body = %+v", got)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"success":true,"errors":[],"messages":[],"result":{"delivered":["ada@example.com"]}}`))}, nil
	})}

	sender := &CloudflareSender{AccountID: "account-123", APIToken: "token-123", APIURL: "https://api.cloudflare.com/client/v4", From: "digest@example.com", Client: client}
	if err := sender.Send(context.Background(), "ada@example.com", "subject", "text", "<p>html</p>"); err != nil {
		t.Fatalf("send: %v", err)
	}
}

func TestCloudflareRejectsAPIError(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader(`{"success":false,"errors":[{"code":10102,"message":"forbidden"}]}`))}, nil
	})}

	sender := &CloudflareSender{AccountID: "account", APIToken: "token", APIURL: "https://api.cloudflare.com/client/v4", From: "digest@example.com", Client: client}
	err := sender.Send(context.Background(), "ada@example.com", "subject", "text", "<p>html</p>")
	if err == nil || !strings.Contains(err.Error(), "10102 forbidden") {
		t.Fatalf("err = %v, want Cloudflare API error", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
