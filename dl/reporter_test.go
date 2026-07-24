package dl

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestJSONReporterWritesEvents(t *testing.T) {
	var buf bytes.Buffer
	reporter := NewJSONReporter(&buf)
	segment := 0

	reporter.Event(Event{
		Type:    EventSegmentFailed,
		Segment: &segment,
		Message: "failed",
	})

	var got Event
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != EventSegmentFailed {
		t.Fatalf("Type = %q, want %q", got.Type, EventSegmentFailed)
	}
	if got.Segment == nil || *got.Segment != 0 {
		t.Fatalf("Segment = %v, want 0", got.Segment)
	}
	if got.Message != "failed" {
		t.Fatalf("Message = %q, want failed", got.Message)
	}
}

func TestHumanReporterCollectsErrorsUntilDownloadDone(t *testing.T) {
	var buf bytes.Buffer
	reporter := NewHumanReporter(&buf)

	reporter.Event(segmentFailedEvent(2, "[failed] seg 2: timeout"))
	if strings.Contains(buf.String(), "timeout") {
		t.Fatalf("error printed before download_done: %q", buf.String())
	}

	reporter.Event(Event{Type: EventDownloadDone})
	if !strings.Contains(buf.String(), "[failed] seg 2: timeout") {
		t.Fatalf("output = %q, want failed segment message", buf.String())
	}
}

func TestHumanReporterNewerEvents(t *testing.T) {
	var buf bytes.Buffer
	reporter := NewHumanReporter(&buf)

	reporter.Event(Event{Type: EventConversionSuspect, Message: "expected 10s, got 3s"})
	reporter.Event(Event{Type: EventTSDeleted, TSFile: "/tmp/movie.ts"})
	reporter.Event(Event{Type: EventError, Message: "failed"})

	output := buf.String()
	for _, want := range []string{
		"[conversion suspect] expected 10s, got 3s",
		"[cleanup] removed /tmp/movie.ts",
		"[error] failed",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output = %q, want %q", output, want)
		}
	}
}

func TestHumanReporterShowsLiveSpeed(t *testing.T) {
	var buf bytes.Buffer
	reporter := NewHumanReporter(&buf)

	ev := progressEvent(EventDownloadProgress, 5, 10)
	ev.Speed = 2 * 1024 * 1024 // 2 MB/s
	reporter.Event(ev)

	if !strings.Contains(buf.String(), "2.00 MB/s") {
		t.Fatalf("output = %q, want live speed 2.00 MB/s", buf.String())
	}
}

func TestHumanReporterKeepsLastSpeedBetweenTicks(t *testing.T) {
	var buf bytes.Buffer
	reporter := NewHumanReporter(&buf)

	withSpeed := progressEvent(EventDownloadProgress, 5, 10)
	withSpeed.Speed = 2 * 1024 * 1024
	reporter.Event(withSpeed)

	buf.Reset()
	// A worker-driven redraw with no speed should still display the last speed.
	reporter.Event(progressEvent(EventDownloadProgress, 6, 10))

	if !strings.Contains(buf.String(), "2.00 MB/s") {
		t.Fatalf("output = %q, want last speed retained", buf.String())
	}
}

func TestHumanReporterPrintsSummary(t *testing.T) {
	var buf bytes.Buffer
	reporter := NewHumanReporter(&buf)

	reporter.Event(Event{
		Type:            EventTaskDone,
		ElapsedSeconds:  83.4,
		DownloadSeconds: 40,
		BytesDownloaded: 20 * 1024 * 1024,
		FileSize:        18 * 1024 * 1024,
		AverageSpeed:    512 * 1024,
	})

	output := buf.String()
	for _, want := range []string{"[summary]", "1m23", "18.00 MB", "512.00 KB/s"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output = %q, want %q", output, want)
		}
	}
	// bytes downloaded stays in the JSON event only, not the human summary line.
	if strings.Contains(output, "20.00 MB") {
		t.Fatalf("human summary should not show total downloaded, got %q", output)
	}
}

func TestHumanReporterSkipsEmptySummary(t *testing.T) {
	var buf bytes.Buffer
	reporter := NewHumanReporter(&buf)

	reporter.Event(Event{Type: EventTaskDone})

	if strings.Contains(buf.String(), "[summary]") {
		t.Fatalf("empty task_done should not print a summary, got %q", buf.String())
	}
}

func TestHumanizeBytes(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.00 KB"},
		{1536, "1.50 KB"},
		{5 * 1024 * 1024, "5.00 MB"},
		{3 * 1024 * 1024 * 1024, "3.00 GB"},
	}
	for _, tt := range tests {
		if got := humanizeBytes(tt.n); got != tt.want {
			t.Fatalf("humanizeBytes(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestHumanizeSpeed(t *testing.T) {
	if got := humanizeSpeed(0); got != "0 B/s" {
		t.Fatalf("humanizeSpeed(0) = %q, want 0 B/s", got)
	}
	if got := humanizeSpeed(2 * 1024 * 1024); got != "2.00 MB/s" {
		t.Fatalf("humanizeSpeed(2MiB) = %q, want 2.00 MB/s", got)
	}
}

func TestHumanizeDuration(t *testing.T) {
	tests := []struct {
		seconds float64
		want    string
	}{
		{0, "0s"},
		{0.25, "250ms"},
		{5, "5s"},
		{83.4, "1m23.4s"},
	}
	for _, tt := range tests {
		if got := humanizeDuration(tt.seconds); got != tt.want {
			t.Fatalf("humanizeDuration(%v) = %q, want %q", tt.seconds, got, tt.want)
		}
	}
}

func TestRenderBarUnlimitedKeepsFullBar(t *testing.T) {
	line := renderBar("Downloading", 0.5, 40, "4/8  1.61 MB/s", 0)
	if strings.Count(line, "■") != 20 {
		t.Fatalf("line = %q, want 20 filled cells for a 40-wide half bar", line)
	}
	if !strings.Contains(line, "1.61 MB/s") {
		t.Fatalf("line = %q, want full suffix", line)
	}
}

func TestRenderBarFitsWithinWidth(t *testing.T) {
	// Across a range of widths the rendered line must never exceed cols-1
	// columns, so it can never wrap onto a second terminal row.
	for cols := 10; cols <= 120; cols++ {
		line := renderBar("Downloading", 1.0, 40, "8/8  1.61 MB/s", cols)
		if n := utf8.RuneCountInString(line); n > cols-1 {
			t.Fatalf("cols=%d: line width %d exceeds budget %d (%q)", cols, n, cols-1, line)
		}
	}
}

func TestRenderBarShrinksBarBeforeText(t *testing.T) {
	// A medium width keeps the bar and full text, just with a narrower bar.
	line := renderBar("Downloading", 1.0, 40, "8/8  1.61 MB/s", 70)
	if !strings.Contains(line, "[") || !strings.Contains(line, "]") {
		t.Fatalf("line = %q, want a bracketed bar to remain", line)
	}
	if !strings.Contains(line, "1.61 MB/s") {
		t.Fatalf("line = %q, want full suffix retained", line)
	}
	if strings.Count(line, "■") >= 40 {
		t.Fatalf("line = %q, want the bar shrunk below 40 cells", line)
	}
}

func TestRenderBarCompactDropsBarKeepsInfo(t *testing.T) {
	// Too narrow for a useful bar, but wide enough to keep the fields: the bar
	// is dropped, the useful info (percent + segments) is kept.
	line := renderBar("Downloading", 0.5, 40, "4/8", 30)
	if strings.ContainsAny(line, "■[]") {
		t.Fatalf("line = %q, want no bar decoration in compact mode", line)
	}
	if !strings.Contains(line, "50.00%") || !strings.Contains(line, "4/8") {
		t.Fatalf("line = %q, want percent and segment count retained", line)
	}
	if n := utf8.RuneCountInString(line); n > 29 {
		t.Fatalf("compact line width %d exceeds budget 29 (%q)", n, line)
	}
}

func TestRenderBarTruncatesWhenExtremelyNarrow(t *testing.T) {
	line := renderBar("Downloading", 1.0, 40, "8/8  1.61 MB/s", 12)
	if utf8.RuneCountInString(line) > 11 {
		t.Fatalf("line = %q exceeds narrow budget", line)
	}
	if !strings.Contains(line, "…") {
		t.Fatalf("line = %q, want an ellipsis marking truncation", line)
	}
}

func TestTerminalWidthNonFileIsZero(t *testing.T) {
	if got := terminalWidth(&bytes.Buffer{}); got != 0 {
		t.Fatalf("terminalWidth(buffer) = %d, want 0", got)
	}
}

func TestProgressEvent(t *testing.T) {
	got := progressEvent(EventDownloadProgress, 3, 10)
	if got.Type != EventDownloadProgress {
		t.Fatalf("Type = %q, want %q", got.Type, EventDownloadProgress)
	}
	if got.Done != 3 || got.Total != 10 {
		t.Fatalf("progress counts = %d/%d, want 3/10", got.Done, got.Total)
	}
	if got.Percent != 30 {
		t.Fatalf("Percent = %v, want 30", got.Percent)
	}
}

func TestSegmentFailedEventIncludesZeroIndex(t *testing.T) {
	got := segmentFailedEvent(0, "failed")
	if got.Segment == nil || *got.Segment != 0 {
		t.Fatalf("Segment = %v, want 0", got.Segment)
	}
}
