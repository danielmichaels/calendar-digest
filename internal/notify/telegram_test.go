package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testToken = "123456:AAHtestsecrettokenvalue"

func newBot(t *testing.T, handler http.HandlerFunc) *Telegram {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &Telegram{Token: testToken, BaseURL: srv.URL, HTTP: srv.Client()}
}

func TestSendMessagePostsTheBotAPIShape(t *testing.T) {
	var gotPath, gotType string
	var gotBody map[string]string

	bot := newBot(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotType = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	if err := bot.SendMessage(t.Context(), "42", "calendar is on fire"); err != nil {
		t.Fatalf("send: %v", err)
	}

	if want := "/bot" + testToken + "/sendMessage"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if gotType != "application/json" {
		t.Errorf("content-type = %q", gotType)
	}
	if gotBody["chat_id"] != "42" || gotBody["text"] != "calendar is on fire" {
		t.Errorf("body = %v", gotBody)
	}
}

// Telegram reports application failures in the body and does not always use a
// non-2xx status for them, so trusting the status alone would call a rejected
// message delivered — and a delivered alert is one nobody sends again today.
func TestSendMessageFailsOnNotOKAtHTTP200(t *testing.T) {
	bot := newBot(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"description":"chat not found"}`))
	})

	err := bot.SendMessage(t.Context(), "42", "hello")
	if err == nil {
		t.Fatal("no error for ok:false")
	}
	if !strings.Contains(err.Error(), "chat not found") {
		t.Errorf("err = %v, want it to carry Telegram's description", err)
	}
}

func TestSendMessageFailsOnAnUnauthorisedBot(t *testing.T) {
	bot := newBot(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"ok":false,"description":"Unauthorized"}`))
	})

	if err := bot.SendMessage(t.Context(), "42", "hello"); err == nil {
		t.Fatal("no error for HTTP 401")
	}
}

// The bot token sits in the path of every request, and transport errors quote
// the URL they failed on. An alert about a broken credential must not itself
// leak a credential into the log.
func TestSendMessageKeepsTheTokenOutOfErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening, so the transport fails and quotes the URL

	bot := &Telegram{Token: testToken, BaseURL: url}
	err := bot.SendMessage(t.Context(), "42", "hello")
	if err == nil {
		t.Fatal("no error against a closed server")
	}
	if strings.Contains(err.Error(), testToken) {
		t.Errorf("error leaks the bot token: %v", err)
	}
}

func TestAlertSendsSubjectAndDetailToTheOperatorChat(t *testing.T) {
	var gotBody map[string]string
	bot := newBot(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	alerter := &TelegramAlerter{Bot: bot, ChatID: "operator"}
	if err := alerter.Alert(t.Context(), "Calendar access refused", "go fix the console"); err != nil {
		t.Fatalf("alert: %v", err)
	}

	if gotBody["chat_id"] != "operator" {
		t.Errorf("chat_id = %q, want the operator chat", gotBody["chat_id"])
	}
	for _, want := range []string{"Calendar access refused", "go fix the console"} {
		if !strings.Contains(gotBody["text"], want) {
			t.Errorf("text = %q, want it to contain %q", gotBody["text"], want)
		}
	}
}
