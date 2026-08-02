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

	if !strings.Contains(buf.String(), "2.0 MB/s") {
		t.Fatalf("output = %q, want live speed 2.0 MB/s", buf.String())
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

	if !strings.Contains(buf.String(), "2.0 MB/s") {
		t.Fatalf("output = %q, want last speed retained", buf.String())
	}
}

func TestHumanReporterShowsEstimateAndETA(t *testing.T) {
	var buf bytes.Buffer
	reporter := NewHumanReporter(&buf)

	ev := progressEvent(EventDownloadProgress, 2, 8)
	ev.Speed = 2 * 1024 * 1024
	ev.EstimatedBytes = 256 * 1024 * 1024
	ev.ETASeconds = 125

	reporter.Event(ev)

	output := buf.String()
	for _, want := range []string{"~256 MB", "ETA 2:05"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output = %q, want %q", output, want)
		}
	}
}

func TestFormatPercentFloorsAndKeepsWidth(t *testing.T) {
	tests := []struct {
		proportion float64
		want       string
	}{
		{0, "  0%"},
		{0.5, " 50%"},
		{1, "100%"},
		// Floors: must not read 100% until the work is actually complete.
		{0.999, " 99%"},
		{0.09, "  9%"},
	}
	for _, tt := range tests {
		if got := formatPercent(tt.proportion); got != tt.want {
			t.Fatalf("formatPercent(%v) = %q, want %q", tt.proportion, got, tt.want)
		}
	}
}

func TestFormatClock(t *testing.T) {
	tests := []struct {
		seconds float64
		want    string
	}{
		{0, "0:00"}, // unlike an ETA, zero is a real position
		{-3, "0:00"},
		{12, "0:12"},
		{72, "1:12"},
		{7264, "2:01:04"},
	}
	for _, tt := range tests {
		if got := formatClock(tt.seconds); got != tt.want {
			t.Fatalf("formatClock(%v) = %q, want %q", tt.seconds, got, tt.want)
		}
	}
}

func TestFormatShare(t *testing.T) {
	if got := formatShare(36, 7264); got != "0.5%" {
		t.Fatalf("formatShare(36, 7264) = %q, want 0.5%%", got)
	}
	// A tiny but real loss must not be rounded away to "0.0%".
	if got := formatShare(1, 100000); got != "<0.1%" {
		t.Fatalf("formatShare(1, 100000) = %q, want <0.1%%", got)
	}
	for _, tt := range [][2]float64{{0, 100}, {10, 0}} {
		if got := formatShare(tt[0], tt[1]); got != "" {
			t.Fatalf("formatShare(%v, %v) = %q, want empty", tt[0], tt[1], got)
		}
	}
}

func TestFormatETA(t *testing.T) {
	tests := []struct {
		seconds float64
		want    string
	}{
		{0, ""},
		{-5, ""},
		{0.3, "0:01"}, // sub-second remainders round up, never to "0:00"
		{42, "0:42"},
		{125, "2:05"},
		{3725, "1:02:05"},
		{25 * 60 * 60, ""}, // absurd values are not quoted
	}
	for _, tt := range tests {
		if got := formatETA(tt.seconds); got != tt.want {
			t.Fatalf("formatETA(%v) = %q, want %q", tt.seconds, got, tt.want)
		}
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
	for _, want := range []string{"[summary]", "1m23", "18.00 MB", "512.0 KB/s"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output = %q, want %q", output, want)
		}
	}
	// bytes downloaded stays in the JSON event only, not the human summary line.
	if strings.Contains(output, "20.00 MB") {
		t.Fatalf("human summary should not show total downloaded, got %q", output)
	}
}

func TestHumanReporterSummaryReportsGapsInTime(t *testing.T) {
	var buf bytes.Buffer
	reporter := NewHumanReporter(&buf)

	reporter.Event(Event{
		Type:             EventTaskDone,
		ElapsedSeconds:   10,
		FileSize:         1024 * 1024,
		AverageSpeed:     1024,
		Incomplete:       true,
		MissingSegments:  []int{2, 9, 10, 11, 12, 17},
		MissingDuration:  36,
		ExpectedDuration: 7264,
		Gaps: []Gap{
			{AtSeconds: 12, MissingSeconds: 6},
			{AtSeconds: 48, MissingSeconds: 24},
			{AtSeconds: 72, MissingSeconds: 6},
		},
	})

	output := buf.String()
	for _, want := range []string{
		"[incomplete] lost 36s of 2:01:04 (0.5%)",
		"[gaps] 0:12 (-6s), 0:48 (-24s), 1:12 (-6s)",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output = %q, want %q", output, want)
		}
	}
	// Segment indexes belong in the JSON events, not the human report.
	if strings.Contains(output, "seg ") {
		t.Fatalf("output = %q, should not name segment indexes", output)
	}
}

func TestHumanReporterReportsUnexplainedShortfall(t *testing.T) {
	var buf bytes.Buffer
	reporter := NewHumanReporter(&buf)

	// 36s from failed segments plus 14s lost somewhere the gaps do not explain.
	reporter.Event(Event{
		Type:                EventTaskDone,
		ElapsedSeconds:      10,
		FileSize:            1024,
		Incomplete:          true,
		MissingDuration:     50,
		UnexplainedDuration: 14,
		ExpectedDuration:    120,
		Gaps: []Gap{
			{AtSeconds: 12, MissingSeconds: 6},
			{AtSeconds: 48, MissingSeconds: 24},
			{AtSeconds: 72, MissingSeconds: 6},
		},
	})

	output := buf.String()
	if !strings.Contains(output, "[incomplete] lost 50s of 2:00 (41.7%)") {
		t.Fatalf("output = %q, want the combined total", output)
	}
	if !strings.Contains(output, "+14s elsewhere") {
		t.Fatalf("output = %q, want the unexplained remainder appended to the gaps", output)
	}
}

func TestHumanReporterReportsShortfallWithoutPositions(t *testing.T) {
	var buf bytes.Buffer
	reporter := NewHumanReporter(&buf)

	// The duration probe found a shortfall but no segment failed, so there are
	// no positions to report.
	reporter.Event(Event{
		Type:                EventTaskDone,
		ElapsedSeconds:      10,
		FileSize:            1024,
		Incomplete:          true,
		MissingDuration:     20,
		UnexplainedDuration: 20,
		ExpectedDuration:    7264,
	})

	output := buf.String()
	if !strings.Contains(output, "[incomplete] lost 20s of 2:01:04 (0.3%), location unknown") {
		t.Fatalf("output = %q, want the shortfall with location unknown", output)
	}
	if strings.Contains(output, "[gaps]") {
		t.Fatalf("output = %q, should not print an empty gaps line", output)
	}
}

func TestHumanReporterReportsLongerThanExpected(t *testing.T) {
	var buf bytes.Buffer
	reporter := NewHumanReporter(&buf)

	// Output longer than the playlist claims is a mismatch, not a loss.
	reporter.Event(Event{
		Type:                EventTaskDone,
		ElapsedSeconds:      10,
		FileSize:            1024,
		Incomplete:          true,
		UnexplainedDuration: -14,
		ExpectedDuration:    120,
		ActualDuration:      134,
	})

	output := buf.String()
	if !strings.Contains(output, "[suspect] duration mismatch: expected 2:00, got 2:14") {
		t.Fatalf("output = %q, want a mismatch line", output)
	}
	if strings.Contains(output, "lost") {
		t.Fatalf("output = %q, must not claim content was lost", output)
	}
}

func TestHumanReporterReportsKeptSegments(t *testing.T) {
	var buf bytes.Buffer
	reporter := NewHumanReporter(&buf)

	reporter.Event(Event{
		Type:             EventTaskDone,
		ElapsedSeconds:   10,
		FileSize:         1024,
		Incomplete:       true,
		MissingDuration:  6,
		ExpectedDuration: 120,
		Gaps:             []Gap{{AtSeconds: 12, MissingSeconds: 6}},
		TSDir:            "/data/example/20260727205231",
	})

	if !strings.Contains(buf.String(), "[segments] kept for re-fetch: /data/example/20260727205231") {
		t.Fatalf("output = %q, want the retained segment folder", buf.String())
	}
}

func TestHumanReporterSummaryHidesSegmentsWhenComplete(t *testing.T) {
	var buf bytes.Buffer
	reporter := NewHumanReporter(&buf)

	reporter.Event(Event{Type: EventTaskDone, ElapsedSeconds: 10, FileSize: 1024})

	if strings.Contains(buf.String(), "incomplete") {
		t.Fatalf("a complete download should not mention gaps, got %q", buf.String())
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
	if got := humanizeSpeed(2 * 1024 * 1024); got != "2.0 MB/s" {
		t.Fatalf("humanizeSpeed(2MiB) = %q, want 2.0 MB/s", got)
	}
}

func TestHumanizeBytesRough(t *testing.T) {
	const gb = 1024 * 1024 * 1024
	tests := []struct {
		name string
		n    int64
		want string
	}{
		// Below GB a decimal adds nothing to an estimate.
		{name: "MB drops decimals", n: 256*1024*1024 + 400*1024, want: "256 MB"},
		{name: "small MB drops decimals", n: 9*1024*1024 + 300*1024, want: "9 MB"},
		{name: "KB drops decimals", n: 5*1024 + 100, want: "5 KB"},
		{name: "KB rounds to nearest", n: 5*1024 + 512, want: "6 KB"},
		// At GB and above a decimal carries real information: 1.9 GB is nearly
		// a gigabyte more than 1 GB, so it must not collapse to "2 GB".
		{name: "GB keeps a decimal", n: 1*gb + 921*1024*1024, want: "1.9 GB"},
		{name: "large GB keeps a decimal", n: 15*gb + 716*1024*1024, want: "15.7 GB"},
		{name: "TB keeps a decimal", n: 2 * 1024 * gb, want: "2.0 TB"},
		{name: "bytes unchanged", n: 512, want: "512 B"},
	}
	for _, tt := range tests {
		if got := humanizeBytesRough(tt.n); got != tt.want {
			t.Fatalf("%s: humanizeBytesRough(%d) = %q, want %q", tt.name, tt.n, got, tt.want)
		}
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

func TestStatusLineFullFormat(t *testing.T) {
	got := statusLine("DL", 0.5, 256*1024*1024, 1.6*1024*1024, 125, 4, 8)
	want := "[DL]  50% of ~256 MB at 1.6 MB/s  ETA 2:05  seg 4/8"
	if got != want {
		t.Fatalf("statusLine =\n  %q\nwant\n  %q", got, want)
	}
}

func TestStatusLineOmitsUnknownFields(t *testing.T) {
	// The figures arrive progressively: before the first sampler tick there is
	// no size, speed or ETA, and the line must still read correctly.
	tests := []struct {
		name     string
		estimate int64
		speed    float64
		eta      float64
		want     string
	}{
		{
			name: "nothing known yet",
			want: "[DL]   0%  seg 0/8",
		},
		{
			name:  "speed only, no estimate yet",
			speed: 1.6 * 1024 * 1024,
			want:  "[DL]   0% at 1.6 MB/s  seg 0/8",
		},
		{
			name:     "size known but stalled, so no ETA",
			estimate: 256 * 1024 * 1024,
			want:     "[DL]   0% of ~256 MB  seg 0/8",
		},
	}
	for _, tt := range tests {
		got := statusLine("DL", 0, tt.estimate, tt.speed, tt.eta, 0, 8)
		if got != tt.want {
			t.Fatalf("%s: statusLine = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestStatusLineMergePhase(t *testing.T) {
	// Merging has no speed, size or ETA — only progress.
	got := statusLine("Merge", 0.5, 0, 0, 0, 4, 8)
	want := "[Merge]  50%  seg 4/8"
	if got != want {
		t.Fatalf("statusLine = %q, want %q", got, want)
	}
}

func TestStatusLineClampsProportion(t *testing.T) {
	if got := statusLine("DL", 1.5, 0, 0, 0, 0, 0); !strings.Contains(got, "100%") {
		t.Fatalf("statusLine = %q, want clamp to 100%%", got)
	}
	if got := statusLine("DL", -1, 0, 0, 0, 0, 0); !strings.Contains(got, "0%") {
		t.Fatalf("statusLine = %q, want clamp to 0%%", got)
	}
}

func TestTruncateRunesFitsBudget(t *testing.T) {
	line := statusLine("DL", 0.5, 256*1024*1024, 1.6*1024*1024, 125, 4, 8)
	// Whatever the terminal width, the drawn line must fit so it cannot wrap.
	for budget := 1; budget <= utf8.RuneCountInString(line)+5; budget++ {
		got := truncateRunes(line, budget)
		if n := utf8.RuneCountInString(got); n > budget {
			t.Fatalf("budget=%d: width %d exceeds it (%q)", budget, n, got)
		}
	}
	if !strings.Contains(truncateRunes(line, 20), "…") {
		t.Fatal("a truncated line should be marked with an ellipsis")
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
