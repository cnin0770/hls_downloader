package tool

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

// Referer and UserAgent, when non-empty, are attached as headers to every
// request made by Get (manifest, key, and segment fetches all go through here).
var (
	Referer    string
	UserAgent  string
	httpClient = &http.Client{
		Timeout: time.Duration(60) * time.Second,
	}
)

func Get(url string) (io.ReadCloser, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if Referer != "" {
		req.Header.Set("Referer", Referer)
	}
	if UserAgent != "" {
		req.Header.Set("User-Agent", UserAgent)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("http error: status code %d", resp.StatusCode)
	}
	return resp.Body, nil
}
