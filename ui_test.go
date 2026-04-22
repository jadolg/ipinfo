package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

const (
	headerContentType  = "Content-Type"
	mimeJSON           = "application/json"
	connectivityErrMsg = "Unable to detect your IP address. Please check your network connectivity."
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
		w.Header().Set(headerContentType, "text/html; charset=utf-8")
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

func waitConnectivityError() chromedp.Action {
	return chromedp.Poll(
		`document.querySelector('#cards p.status') !== null`,
		nil,
	)
}

func TestUIDisplaysIPOnSuccess(t *testing.T) {
	url := newUIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(headerContentType, mimeJSON)
		_, _ = w.Write([]byte(`{
			"IPAddress":"203.0.113.1",
			"ISP":"Example ISP","TorExit":false,
			"City":"Berlin","Country":"Germany","CountryCode":"DE",
			"Location":"Berlin, BE, Germany"
		}`))
	})

	var ipText, ispText string
	err := chromedp.Run(browserCtx(t),
		chromedp.Navigate(url),
		chromedp.WaitVisible(`#rows-IP`),
		chromedp.Text(`#ip-IP`, &ipText),
		chromedp.Text(`#isp-IP`, &ispText),
	)
	if err != nil {
		t.Fatal(err)
	}
	if ipText != "203.0.113.1" {
		t.Errorf("IP = %q, want 203.0.113.1", ipText)
	}
	if ispText != "Example ISP" {
		t.Errorf("ISP = %q, want Example ISP", ispText)
	}
}

func TestUIShowsTorBadge(t *testing.T) {
	url := newUIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(headerContentType, mimeJSON)
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

func TestUIShowsConnectivityErrorOnBadResponse(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{
			name: "http failure",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			},
		},
		{
			name: "non-json response",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set(headerContentType, "text/html")
				_, _ = w.Write([]byte(`<html><body>not json</body></html>`))
			},
		},
		{
			name: "wrong shape",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set(headerContentType, mimeJSON)
				_, _ = w.Write([]byte(`{"unexpected":"field"}`))
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			url := newUIServer(t, tc.handler)
			var msgText string
			err := chromedp.Run(browserCtx(t),
				chromedp.Navigate(url),
				waitConnectivityError(),
				chromedp.Text(`#cards p.status`, &msgText),
			)
			if err != nil {
				t.Fatal(err)
			}
			if msgText != connectivityErrMsg {
				t.Errorf("connectivity message = %q", msgText)
			}
		})
	}
}

func TestUIHidesFailedCardWhenOtherSucceeds(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/json4", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(headerContentType, mimeJSON)
		_, _ = w.Write([]byte(`{"IPAddress":"203.0.113.1"}`))
	})
	mux.HandleFunc("/json6", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no IPv6", http.StatusServiceUnavailable)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set(headerContentType, "text/html; charset=utf-8")
		_ = indexTmpl.Execute(w, indexConfig{
			IPv4URL: "http://" + r.Host + "/json4",
			IPv6URL: "http://" + r.Host + "/json6",
		})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	var ipText string
	var noConnMsg bool
	err := chromedp.Run(browserCtx(t),
		chromedp.Navigate(ts.URL),
		chromedp.WaitVisible(`#rows-IPv4`),
		chromedp.WaitNotPresent(`#ip-IPv6`),
		chromedp.Text(`#ip-IPv4`, &ipText),
		chromedp.Evaluate(`document.querySelector('#cards p.status') === null`, &noConnMsg),
	)
	if err != nil {
		t.Fatal(err)
	}
	if ipText != "203.0.113.1" {
		t.Errorf("IPv4 card IP = %q, want 203.0.113.1", ipText)
	}
	if !noConnMsg {
		t.Error("connectivity error message should not be shown when one card succeeds")
	}
}
