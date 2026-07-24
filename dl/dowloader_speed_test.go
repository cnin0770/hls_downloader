package dl

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// throttledHLSServer serves a small playlist whose segments are drip-fed so the
// whole download reliably spans more than one sampler interval (1s), letting the
// live-speed sampler fire at least once mid-download regardless of host speed.
func throttledHLSServer(segCount, segSize, chunk int, chunkDelay time.Duration) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/index.m3u8", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "#EXTM3U")
		fmt.Fprintln(w, "#EXT-X-VERSION:3")
		fmt.Fprintln(w, "#EXT-X-TARGETDURATION:6")
		fmt.Fprintln(w, "#EXT-X-MEDIA-SEQUENCE:0")
		for i := 0; i < segCount; i++ {
			fmt.Fprintln(w, "#EXTINF:6.0,")
			fmt.Fprintf(w, "seg%d.ts\n", i)
		}
		fmt.Fprintln(w, "#EXT-X-ENDLIST")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		flusher, _ := w.(http.Flusher)
		payload := make([]byte, chunk)
		payload[0] = 0x47 // TS sync byte
		w.Header().Set("Content-Length", fmt.Sprint(segSize))
		w.WriteHeader(http.StatusOK)
		for sent := 0; sent < segSize; {
			n := chunk
			if segSize-sent < n {
				n = segSize - sent
			}
			if _, err := w.Write(payload[:n]); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
			sent += n
			time.Sleep(chunkDelay)
		}
	})
	return httptest.NewServer(mux)
}

func TestLiveSpeedSampledMidDownload(t *testing.T) {
	// 6 segments * 16 chunks * 40ms = ~640ms/segment; with concurrency 2 the
	// download spans ~1.9s, guaranteeing at least one 1s sampler tick.
	server := throttledHLSServer(6, 256*1024, 16*1024, 40*time.Millisecond)
	defer server.Close()

	downloader, err := NewTask(t.TempDir(), server.URL+"/index.m3u8", "livespeed")
	if err != nil {
		t.Fatal(err)
	}
	rec := &recordingReporter{}
	downloader.SetReporter(rec)

	if err := downloader.Start(2); err != nil {
		t.Fatal(err)
	}

	var sawLiveSpeed bool
	var done Event
	for _, ev := range rec.snapshot() {
		if ev.Type == EventDownloadProgress && ev.Speed > 0 {
			sawLiveSpeed = true
		}
		if ev.Type == EventTaskDone {
			done = ev
		}
	}

	if !sawLiveSpeed {
		t.Fatal("expected at least one download_progress event carrying Speed > 0")
	}
	if done.BytesDownloaded <= 0 {
		t.Fatalf("task_done bytes_downloaded = %d, want > 0", done.BytesDownloaded)
	}
	if done.AverageSpeed <= 0 {
		t.Fatalf("task_done average_speed = %v, want > 0", done.AverageSpeed)
	}
	if done.FileSize <= 0 {
		t.Fatalf("task_done file_size = %d, want > 0", done.FileSize)
	}
}
