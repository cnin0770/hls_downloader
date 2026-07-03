package tool

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestGet(t *testing.T) {
	oldReferer := Referer
	oldUserAgent := UserAgent
	oldHTTPClient := httpClient
	defer func() {
		Referer = oldReferer
		UserAgent = oldUserAgent
		httpClient = oldHTTPClient
	}()

	Referer = "https://example.com/video"
	UserAgent = "m3u8-test"
	httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if got := req.Header.Get("Referer"); got != Referer {
				t.Fatalf("Referer = %q, want %q", got, Referer)
			}
			if got := req.Header.Get("User-Agent"); got != UserAgent {
				t.Fatalf("User-Agent = %q, want %q", got, UserAgent)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("ok")),
			}, nil
		}),
	}

	body, err := Get("https://example.com/index.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	bytes, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(bytes) != "ok" {
		t.Fatalf("body = %q, want ok", string(bytes))
	}
}

func TestGetReturnsHTTPError(t *testing.T) {
	oldHTTPClient := httpClient
	defer func() {
		httpClient = oldHTTPClient
	}()
	httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusTeapot,
				Body:       io.NopCloser(strings.NewReader("nope")),
			}, nil
		}),
	}

	body, err := Get("https://example.com/index.m3u8")
	if body != nil {
		_ = body.Close()
	}
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "status code 418") {
		t.Fatalf("error = %q, want status code 418", err.Error())
	}
}

func TestGetReturnsTransportError(t *testing.T) {
	oldHTTPClient := httpClient
	defer func() {
		httpClient = oldHTTPClient
	}()
	httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return nil, errors.New("network down")
		}),
	}

	body, err := Get("https://example.com/index.m3u8")
	if body != nil {
		_ = body.Close()
	}
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "network down") {
		t.Fatalf("error = %q, want network down", err.Error())
	}
}
