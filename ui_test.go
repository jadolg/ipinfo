package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

func newUIServer(t *testing.T, jsonHandler http.HandlerFunc) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/json", jsonHandler)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = indexTmpl.Execute(w, indexConfig{})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts.URL
}

func browserCtx(t *testing.T) context.Context {
	t.Helper()
	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.NoSandbox)
	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	t.Cleanup(cancel)
	ctx, cancel := chromedp.NewContext(allocCtx)
	t.Cleanup(cancel)
	ctx, cancel = context.WithTimeout(ctx, 15*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func waitStatus(not string) chromedp.Action {
	return chromedp.Poll(
		`document.getElementById('status-IP') &&
		 document.getElementById('status-IP').textContent !== '`+not+`'`,
		nil,
	)
}

func TestUIDisplaysIPOnSuccess(t *testing.T) {
	url := newUIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"IPAddress":"203.0.113.1","Hostname":"host.example.com",
			"ISP":"Example ISP","TorExit":false,
			"City":"Berlin","Country":"Germany","CountryCode":"DE",
			"Location":"Berlin, BE, Germany"
		}`))
	})

	var ipText, hostnameText string
	err := chromedp.Run(browserCtx(t),
		chromedp.Navigate(url),
		chromedp.WaitVisible(`#rows-IP`),
		chromedp.Text(`#ip-IP`, &ipText),
		chromedp.Text(`#hostname-IP`, &hostnameText),
	)
	if err != nil {
		t.Fatal(err)
	}
	if ipText != "203.0.113.1" {
		t.Errorf("IP = %q, want 203.0.113.1", ipText)
	}
	if hostnameText != "host.example.com" {
		t.Errorf("Hostname = %q, want host.example.com", hostnameText)
	}
}

func TestUIShowsTorBadge(t *testing.T) {
	url := newUIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"IPAddress":"1.2.3.4","TorExit":true}`))
	})

	var torText string
	err := chromedp.Run(browserCtx(t),
		chromedp.Navigate(url),
		chromedp.WaitVisible(`#rows-IP`),
		chromedp.Text(`#tor-IP`, &torText),
	)
	if err != nil {
		t.Fatal(err)
	}
	if torText != "Yes" {
		t.Errorf("Tor badge = %q, want Yes", torText)
	}
}

func TestUIShowsErrorOnHTTPFailure(t *testing.T) {
	url := newUIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
	})

	var statusText string
	err := chromedp.Run(browserCtx(t),
		chromedp.Navigate(url),
		waitStatus("Loading\u2026"),
		chromedp.Text(`#status-IP`, &statusText),
	)
	if err != nil {
		t.Fatal(err)
	}
	if statusText != "Server returned HTTP 503" {
		t.Errorf("status = %q, want \"Server returned HTTP 503\"", statusText)
	}
}

func TestUIShowsErrorOnNonJSON(t *testing.T) {
	url := newUIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body>not json</body></html>`))
	})

	var statusText string
	err := chromedp.Run(browserCtx(t),
		chromedp.Navigate(url),
		waitStatus("Loading\u2026"),
		chromedp.Text(`#status-IP`, &statusText),
	)
	if err != nil {
		t.Fatal(err)
	}
	if statusText != "Response is not valid JSON" {
		t.Errorf("status = %q, want \"Response is not valid JSON\"", statusText)
	}
}

func TestUIShowsErrorOnWrongShape(t *testing.T) {
	url := newUIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"unexpected":"field"}`))
	})

	var statusText string
	err := chromedp.Run(browserCtx(t),
		chromedp.Navigate(url),
		waitStatus("Loading\u2026"),
		chromedp.Text(`#status-IP`, &statusText),
	)
	if err != nil {
		t.Fatal(err)
	}
	if statusText != "Unexpected response shape" {
		t.Errorf("status = %q, want \"Unexpected response shape\"", statusText)
	}
}
