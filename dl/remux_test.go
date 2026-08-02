package dl

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/cnin0770/hls_downloader/parse"
)

func TestFFmpegRemuxerBuildsCommand(t *testing.T) {
	var gotName string
	var gotArgs []string
	remuxer := FFmpegRemuxer{
		LookPath: func(name string) (string, error) {
			if name != "ffmpeg" {
				t.Fatalf("LookPath(%q), want ffmpeg", name)
			}
			return "/usr/local/bin/ffmpeg", nil
		},
		Run: func(name string, args ...string) error {
			gotName = name
			gotArgs = append([]string(nil), args...)
			return nil
		},
	}

	if err := remuxer.RemuxTS("input.ts", "output.mp4"); err != nil {
		t.Fatal(err)
	}
	if gotName != "/usr/local/bin/ffmpeg" {
		t.Fatalf("command name = %q, want /usr/local/bin/ffmpeg", gotName)
	}
	wantArgs := []string{"-y", "-i", "input.ts", "-c", "copy", "-movflags", "+faststart", "output.mp4"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestFFmpegRemuxerMissingFFmpeg(t *testing.T) {
	remuxer := FFmpegRemuxer{
		LookPath: func(name string) (string, error) {
			return "", errors.New("not found")
		},
	}

	err := remuxer.RemuxTS("input.ts", "output.mp4")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), conversionToolMissing) {
		t.Fatalf("error = %q, want %q", err.Error(), conversionToolMissing)
	}
}

func TestRunCommandQuietIncludesStderrOnFailure(t *testing.T) {
	err := runCommandQuiet("/bin/sh", "-c", "echo broken >&2; exit 2")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Fatalf("error = %q, want stderr text", err.Error())
	}
}

func TestMP4Filename(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "/tmp/movie.ts", want: "/tmp/movie.mp4"},
		{name: "/tmp/movie.TS", want: "/tmp/movie.mp4"},
		{name: "/tmp/movie", want: "/tmp/movie.mp4"},
	}
	for _, tt := range tests {
		got := mp4Filename(tt.name)
		if got != tt.want {
			t.Fatalf("mp4Filename(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

type fakeRemuxer struct {
	input  string
	output string
	err    error
}

func (r *fakeRemuxer) RemuxTS(input string, output string) error {
	r.input = input
	r.output = output
	if r.err != nil {
		return r.err
	}
	return os.WriteFile(output, []byte("mp4"), 0644)
}

type recordingReporter struct {
	mu     sync.Mutex
	events []Event
}

func (r *recordingReporter) Event(ev Event) {
	r.mu.Lock()
	r.events = append(r.events, ev)
	r.mu.Unlock()
}

func (r *recordingReporter) Close() {}

// snapshot returns a copy of the recorded events, safe to read after a
// concurrent download run.
func (r *recordingReporter) snapshot() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Event, len(r.events))
	copy(out, r.events)
	return out
}

type fakeDurationProber struct {
	duration float64
	err      error
}

func (p fakeDurationProber) Duration(path string) (float64, error) {
	if p.err != nil {
		return 0, p.err
	}
	return p.duration, nil
}

func TestConvertKeepsTSByDefault(t *testing.T) {
	dir := t.TempDir()
	tsFile := filepath.Join(dir, "movie.ts")
	if err := os.WriteFile(tsFile, []byte("ts"), 0644); err != nil {
		t.Fatal(err)
	}
	remuxer := &fakeRemuxer{}
	reporter := &recordingReporter{}
	downloader := &Downloader{
		keepTS:   true,
		remuxer:  remuxer,
		prober:   fakeDurationProber{duration: 100},
		reporter: reporter,
	}

	conv, err := downloader.convert(tsFile, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if conv.mp4File != filepath.Join(dir, "movie.mp4") {
		t.Fatalf("mp4File = %q, want movie.mp4", conv.mp4File)
	}
	if _, err := os.Stat(tsFile); err != nil {
		t.Fatalf("TS file should be kept: %s", err)
	}
	if remuxer.input != tsFile || remuxer.output != conv.mp4File {
		t.Fatalf("remux input/output = %q/%q, want %q/%q", remuxer.input, remuxer.output, tsFile, conv.mp4File)
	}
	if !hasEvent(reporter.events, EventConversionStarted) || !hasEvent(reporter.events, EventConversionDone) {
		t.Fatalf("events = %+v, want conversion started and done", reporter.events)
	}
}

func TestConvertDeletesTSWhenKeepTSFalse(t *testing.T) {
	dir := t.TempDir()
	tsFile := filepath.Join(dir, "movie.ts")
	if err := os.WriteFile(tsFile, []byte("ts"), 0644); err != nil {
		t.Fatal(err)
	}
	reporter := &recordingReporter{}
	downloader := &Downloader{
		keepTS:   false,
		remuxer:  &fakeRemuxer{},
		prober:   fakeDurationProber{duration: 100},
		reporter: reporter,
	}

	if _, err := downloader.convert(tsFile, false, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tsFile); !os.IsNotExist(err) {
		t.Fatalf("TS file still exists, stat err = %v", err)
	}
	if !hasEvent(reporter.events, EventTSDeleted) {
		t.Fatalf("events = %+v, want TS deleted", reporter.events)
	}
}

func TestConvertReportsFailureAndKeepsTS(t *testing.T) {
	dir := t.TempDir()
	tsFile := filepath.Join(dir, "movie.ts")
	if err := os.WriteFile(tsFile, []byte("ts"), 0644); err != nil {
		t.Fatal(err)
	}
	reporter := &recordingReporter{}
	downloader := &Downloader{
		keepTS:   false,
		remuxer:  &fakeRemuxer{err: errors.New("boom")},
		prober:   fakeDurationProber{duration: 100},
		reporter: reporter,
	}

	_, err := downloader.convert(tsFile, false, 0)
	if err == nil {
		t.Fatal("expected error")
	}
	if _, err := os.Stat(tsFile); err != nil {
		t.Fatalf("TS file should be kept after conversion failure: %s", err)
	}
	if !hasEvent(reporter.events, EventConversionFailed) {
		t.Fatalf("events = %+v, want conversion failed", reporter.events)
	}
}

func TestConvertMarksSuspectAndKeepsTS(t *testing.T) {
	dir := t.TempDir()
	tsFile := filepath.Join(dir, "movie.ts")
	if err := os.WriteFile(tsFile, []byte("ts"), 0644); err != nil {
		t.Fatal(err)
	}
	reporter := &recordingReporter{}
	downloader := &Downloader{
		keepTS:   false,
		remuxer:  &fakeRemuxer{},
		prober:   fakeDurationProber{duration: 200},
		reporter: reporter,
		result: &parse.Result{
			M3u8: &parse.M3u8{
				Segments: []*parse.Segment{
					{Duration: 60},
					{Duration: 40},
				},
			},
		},
	}

	conv, err := downloader.convert(tsFile, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if conv.mp4File != filepath.Join(dir, "movie.000140.mp4") {
		t.Fatalf("mp4File = %q, want movie.000140.mp4", conv.mp4File)
	}
	if _, err := os.Stat(tsFile); err != nil {
		t.Fatalf("TS file should be kept after suspect conversion: %s", err)
	}
	if !hasEvent(reporter.events, EventConversionSuspect) {
		t.Fatalf("events = %+v, want conversion suspect", reporter.events)
	}
}

func TestCleanupTSReportsWarningWhenRemoveFails(t *testing.T) {
	reporter := &recordingReporter{}
	downloader := &Downloader{reporter: reporter}

	downloader.cleanupTS(filepath.Join(t.TempDir(), "missing.ts"))

	if !hasEvent(reporter.events, EventWarning) {
		t.Fatalf("events = %+v, want warning", reporter.events)
	}
	if hasEvent(reporter.events, EventTSDeleted) {
		t.Fatalf("events = %+v, did not expect TS deleted", reporter.events)
	}
}

func hasEvent(events []Event, eventType string) bool {
	for _, ev := range events {
		if ev.Type == eventType {
			return true
		}
	}
	return false
}
