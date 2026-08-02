package dl

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"time"
)

var segPattern = regexp.MustCompile(`seg(\d+)\.ts`)

// flakyHLSServer serves a playlist whose segments succeed immediately, except
// for the segments in failures: each of those returns 404 for the given number
// of attempts (a negative count fails forever). It records how many times every
// segment was requested.
type flakyHLSServer struct {
	*httptest.Server
	mu       sync.Mutex
	attempts map[int]int
}

func newFlakyHLSServer(segCount int, failures map[int]int) *flakyHLSServer {
	s := &flakyHLSServer{attempts: make(map[int]int)}
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
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		match := segPattern.FindStringSubmatch(r.URL.Path)
		if match == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		idx, _ := strconv.Atoi(match[1])

		s.mu.Lock()
		s.attempts[idx]++
		seen := s.attempts[idx]
		s.mu.Unlock()

		if budget, ok := failures[idx]; ok && (budget < 0 || seen <= budget) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte{0x47, 0x01, 0x02, 0x03})
	})
	s.Server = httptest.NewServer(mux)
	return s
}

func (s *flakyHLSServer) attemptsFor(idx int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts[idx]
}

// runWithin fails the test if the download has not returned in time, rather
// than letting a regression hang the whole suite.
func runWithin(t *testing.T, limit time.Duration, run func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- run() }()
	select {
	case err := <-done:
		return err
	case <-time.After(limit):
		t.Fatalf("download did not finish within %s: it is hanging", limit)
		return nil
	}
}

// A permanently failing segment used to be re-queued forever, so the download
// never terminated. It must now give up, finish, and report the result as
// incomplete rather than passing it off as a success.
func TestPermanentlyFailingSegmentTerminates(t *testing.T) {
	server := newFlakyHLSServer(6, map[int]int{3: -1})
	defer server.Close()

	downloader, err := NewTask(t.TempDir(), server.URL+"/index.m3u8", "flaky")
	if err != nil {
		t.Fatal(err)
	}
	rec := &recordingReporter{}
	downloader.SetReporter(rec)
	downloader.SetRetries(2)

	if err := runWithin(t, 30*time.Second, func() error {
		return downloader.Start(2)
	}); err != nil {
		t.Fatal(err)
	}

	failed := downloader.failedSegments()
	if len(failed) != 1 || failed[0] != 3 {
		t.Fatalf("failedSegments = %v, want [3]", failed)
	}
	if got := server.attemptsFor(3); got != 2 {
		t.Fatalf("segment 3 was attempted %d times, want 2 (the retry cap)", got)
	}

	var done Event
	for _, ev := range rec.snapshot() {
		if ev.Type == EventTaskDone {
			done = ev
		}
	}
	if !done.Incomplete {
		t.Fatal("task_done should be marked incomplete when a segment was given up on")
	}
	if len(done.MissingSegments) != 1 || done.MissingSegments[0] != 3 {
		t.Fatalf("task_done missing_segments = %v, want [3]", done.MissingSegments)
	}
	// The other five segments should still have been merged into an output file.
	if done.TSFile == "" {
		t.Fatal("expected the surviving segments to still be merged into a file")
	}
}

// A segment that fails then recovers must be retried and the run must complete
// cleanly, with nothing reported as missing.
func TestTransientFailureIsRetriedAndSucceeds(t *testing.T) {
	server := newFlakyHLSServer(4, map[int]int{1: 2}) // fails twice, then works
	defer server.Close()

	downloader, err := NewTask(t.TempDir(), server.URL+"/index.m3u8", "transient")
	if err != nil {
		t.Fatal(err)
	}
	rec := &recordingReporter{}
	downloader.SetReporter(rec)
	downloader.SetRetries(4)

	if err := runWithin(t, 30*time.Second, func() error {
		return downloader.Start(2)
	}); err != nil {
		t.Fatal(err)
	}

	if failed := downloader.failedSegments(); len(failed) != 0 {
		t.Fatalf("failedSegments = %v, want none: the segment recovered", failed)
	}
	if got := server.attemptsFor(1); got != 3 {
		t.Fatalf("segment 1 was attempted %d times, want 3 (two failures then success)", got)
	}
	for _, ev := range rec.snapshot() {
		if ev.Type == EventTaskDone && ev.Incomplete {
			t.Fatal("a recovered download must not be marked incomplete")
		}
	}
}

// -keep-segments preserves the downloaded parts when the run is incomplete, so
// the missing ones can be re-fetched by index and merged in.
func TestKeepSegmentsRetainsPartsWhenIncomplete(t *testing.T) {
	server := newFlakyHLSServer(5, map[int]int{2: -1})
	defer server.Close()

	downloader, err := NewTask(t.TempDir(), server.URL+"/index.m3u8", "kept")
	if err != nil {
		t.Fatal(err)
	}
	rec := &recordingReporter{}
	downloader.SetReporter(rec)
	downloader.SetRetries(1)
	downloader.SetKeepSegments(true)

	if err := runWithin(t, 30*time.Second, func() error { return downloader.Start(2) }); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(downloader.tsFolder)
	if err != nil {
		t.Fatalf("segment folder should have been kept: %s", err)
	}
	// The four that downloaded are there; the failed one is not.
	if len(entries) != 4 {
		t.Fatalf("kept %d segment files, want 4", len(entries))
	}
	if _, err := os.Stat(filepath.Join(downloader.tsFolder, tsFilename(2))); !os.IsNotExist(err) {
		t.Fatalf("failed segment should not exist, stat err = %v", err)
	}
	// The report points at the folder so it can be found.
	var done Event
	for _, ev := range rec.snapshot() {
		if ev.Type == EventTaskDone {
			done = ev
		}
	}
	if done.TSDir != downloader.tsFolder {
		t.Fatalf("task_done ts_dir = %q, want %q", done.TSDir, downloader.tsFolder)
	}
}

// A clean download always cleans up, even with -keep-segments: keeping a full
// second copy of every segment would just double the space used.
func TestKeepSegmentsCleansUpWhenComplete(t *testing.T) {
	server := newFlakyHLSServer(4, nil)
	defer server.Close()

	downloader, err := NewTask(t.TempDir(), server.URL+"/index.m3u8", "clean")
	if err != nil {
		t.Fatal(err)
	}
	rec := &recordingReporter{}
	downloader.SetReporter(rec)
	downloader.SetKeepSegments(true)

	if err := runWithin(t, 30*time.Second, func() error { return downloader.Start(2) }); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(downloader.tsFolder); !os.IsNotExist(err) {
		t.Fatalf("segment folder should have been removed, stat err = %v", err)
	}
	for _, ev := range rec.snapshot() {
		if ev.Type == EventTaskDone && ev.TSDir != "" {
			t.Fatalf("task_done should not advertise a kept folder, got %q", ev.TSDir)
		}
	}
}

// Without the flag the parts are discarded even when the run is incomplete.
func TestSegmentsDiscardedByDefault(t *testing.T) {
	server := newFlakyHLSServer(4, map[int]int{1: -1})
	defer server.Close()

	downloader, err := NewTask(t.TempDir(), server.URL+"/index.m3u8", "discard")
	if err != nil {
		t.Fatal(err)
	}
	downloader.SetReporter(&recordingReporter{})
	downloader.SetRetries(1)

	if err := runWithin(t, 30*time.Second, func() error { return downloader.Start(2) }); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(downloader.tsFolder); !os.IsNotExist(err) {
		t.Fatalf("segment folder should have been removed, stat err = %v", err)
	}
}

// Every segment failing is the pathological case: it must still terminate.
func TestAllSegmentsFailingStillTerminates(t *testing.T) {
	server := newFlakyHLSServer(3, map[int]int{0: -1, 1: -1, 2: -1})
	defer server.Close()

	downloader, err := NewTask(t.TempDir(), server.URL+"/index.m3u8", "allfail")
	if err != nil {
		t.Fatal(err)
	}
	downloader.SetReporter(&recordingReporter{})
	downloader.SetRetries(1)

	if err := runWithin(t, 30*time.Second, func() error {
		return downloader.Start(2)
	}); err != nil {
		t.Fatal(err)
	}

	if failed := downloader.failedSegments(); len(failed) != 3 {
		t.Fatalf("failedSegments = %v, want all three", failed)
	}
}
