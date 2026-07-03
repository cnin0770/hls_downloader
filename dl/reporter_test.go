package dl

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
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
