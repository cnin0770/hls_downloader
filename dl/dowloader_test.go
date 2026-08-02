package dl

import (
	"reflect"
	"regexp"
	"testing"

	"github.com/cnin0770/hls_downloader/parse"
)

func TestCompletedFractionPrefersDuration(t *testing.T) {
	// 30s of a 120s playlist is 25% done, even though 1 of 2 segments (50%)
	// has finished — duration weighting is what makes the size estimate track
	// a variable-bitrate stream.
	got := completedFraction(30, 120, 1, 2)
	if got != 0.25 {
		t.Fatalf("completedFraction = %v, want 0.25", got)
	}
}

func TestCompletedFractionFallsBackToSegmentCount(t *testing.T) {
	got := completedFraction(0, 0, 3, 12)
	if got != 0.25 {
		t.Fatalf("completedFraction = %v, want 0.25", got)
	}
}

func TestCompletedFractionClamps(t *testing.T) {
	if got := completedFraction(200, 100, 0, 0); got != 1 {
		t.Fatalf("completedFraction = %v, want clamp to 1", got)
	}
	if got := completedFraction(0, 0, 0, 0); got != 0 {
		t.Fatalf("completedFraction = %v, want 0 with no data", got)
	}
}

func TestExtrapolateTotalBytes(t *testing.T) {
	if got := extrapolateTotalBytes(25*1024*1024, 0.25); got != 100*1024*1024 {
		t.Fatalf("extrapolateTotalBytes = %d, want 100 MiB", got)
	}
	// Nothing downloaded yet, or no progress: refuse to guess.
	if got := extrapolateTotalBytes(0, 0.5); got != 0 {
		t.Fatalf("extrapolateTotalBytes = %d, want 0", got)
	}
	if got := extrapolateTotalBytes(1024, 0); got != 0 {
		t.Fatalf("extrapolateTotalBytes = %d, want 0", got)
	}
	// Complete: the estimate is simply what was downloaded.
	if got := extrapolateTotalBytes(4096, 1); got != 4096 {
		t.Fatalf("extrapolateTotalBytes = %d, want 4096", got)
	}
}

func TestEstimateETASeconds(t *testing.T) {
	if got := estimateETASeconds(10*1024*1024, 1024*1024); got != 10 {
		t.Fatalf("estimateETASeconds = %v, want 10", got)
	}
	if got := estimateETASeconds(0, 1024); got != 0 {
		t.Fatalf("estimateETASeconds = %v, want 0 when nothing remains", got)
	}
	if got := estimateETASeconds(1024, 0); got != 0 {
		t.Fatalf("estimateETASeconds = %v, want 0 when speed is unknown", got)
	}
}

func TestOutputFilename(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "testName", want: "testName.ts"},
		{name: "testName.ts", want: "testName.ts"},
		{name: " nested ", want: "nested.ts"},
		{name: "/tmp/video", want: "video.ts"},
	}

	for _, tt := range tests {
		got := outputFilename(tt.name, "20260703123456")
		if got != tt.want {
			t.Fatalf("outputFilename(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestOutputFilenameDefaultName(t *testing.T) {
	got := outputFilename("", "20260703123456")
	if got != "20260703123456.ts" {
		t.Fatalf("outputFilename(\"\") = %q, want timestamp filename", got)
	}
}

func TestTaskTimestampFormat(t *testing.T) {
	got := taskTimestamp()
	if ok, err := regexp.MatchString(`^\d{14}$`, got); err != nil {
		t.Fatal(err)
	} else if !ok {
		t.Fatalf("taskTimestamp() = %q, want 14 digits", got)
	}
}

func TestTSFilename(t *testing.T) {
	got := tsFilename(42)
	if got != "42.ts" {
		t.Fatalf("tsFilename(42) = %q, want 42.ts", got)
	}
}

func TestSetRetriesFloorsAtOne(t *testing.T) {
	downloader := &Downloader{}
	for _, in := range []int{0, -5} {
		downloader.SetRetries(in)
		if downloader.retries != 1 {
			t.Fatalf("SetRetries(%d) = %d, want 1", in, downloader.retries)
		}
	}
	downloader.SetRetries(5)
	if downloader.retries != 5 {
		t.Fatalf("SetRetries(5) = %d, want 5", downloader.retries)
	}
}

func TestRetryDelayBacksOffAndCaps(t *testing.T) {
	if got := retryDelay(1); got != retryBackoff {
		t.Fatalf("retryDelay(1) = %v, want %v", got, retryBackoff)
	}
	if got := retryDelay(2); got != 2*retryBackoff {
		t.Fatalf("retryDelay(2) = %v, want %v", got, 2*retryBackoff)
	}
	// Long runs must not overflow into a negative or absurd sleep.
	for _, attempt := range []int{6, 20, 64, 100} {
		if got := retryDelay(attempt); got != maxBackoff {
			t.Fatalf("retryDelay(%d) = %v, want cap %v", attempt, got, maxBackoff)
		}
	}
}

func TestGapsFromFailures(t *testing.T) {
	// Twenty 6-second segments, matching the worked example: losing 2, 9-12
	// and 17 out of a 120s playlist.
	segments := make([]*parse.Segment, 20)
	for i := range segments {
		segments[i] = &parse.Segment{Duration: 6}
	}

	gaps, lost := gapsFromFailures(segments, []int{2, 9, 10, 11, 12, 17})

	if lost != 36 {
		t.Fatalf("lost = %v, want 36 (six 6s segments)", lost)
	}
	want := []Gap{
		// seg 2: two segments survive before it -> 12s in.
		{AtSeconds: 12, MissingSeconds: 6},
		// seg 9-12 collapse into one point: 8 surviving segments precede them,
		// so the jump lands at 48s, not at the source position of 54s.
		{AtSeconds: 48, MissingSeconds: 24},
		// seg 17: 12 survivors precede it -> 72s.
		{AtSeconds: 72, MissingSeconds: 6},
	}
	if !reflect.DeepEqual(gaps, want) {
		t.Fatalf("gaps = %+v, want %+v", gaps, want)
	}
}

func TestGapsFromFailuresEdgeCases(t *testing.T) {
	segments := []*parse.Segment{
		{Duration: 10},
		{Duration: 10},
		{Duration: 10},
	}

	if gaps, lost := gapsFromFailures(segments, nil); gaps != nil || lost != 0 {
		t.Fatalf("no failures should give no gaps, got %+v / %v", gaps, lost)
	}

	// A gap at the very start sits at 0:00, which is a real position.
	gaps, lost := gapsFromFailures(segments, []int{0})
	if len(gaps) != 1 || gaps[0].AtSeconds != 0 || gaps[0].MissingSeconds != 10 {
		t.Fatalf("leading gap = %+v, want one at 0s of 10s", gaps)
	}
	if lost != 10 {
		t.Fatalf("lost = %v, want 10", lost)
	}

	// Everything missing: one gap at 0 covering the whole playlist.
	gaps, lost = gapsFromFailures(segments, []int{0, 1, 2})
	if len(gaps) != 1 || gaps[0].AtSeconds != 0 || gaps[0].MissingSeconds != 30 {
		t.Fatalf("total loss = %+v, want a single 30s gap at 0", gaps)
	}
	if lost != 30 {
		t.Fatalf("lost = %v, want 30", lost)
	}

	// A nil segment must not panic or corrupt the timeline.
	if _, lost := gapsFromFailures([]*parse.Segment{nil, {Duration: 5}}, []int{0}); lost != 0 {
		t.Fatalf("nil segment lost = %v, want 0", lost)
	}
}

func TestSummarizeSegments(t *testing.T) {
	tests := []struct {
		name string
		in   []int
		want string
	}{
		{name: "empty", in: nil, want: ""},
		{name: "single", in: []int{7}, want: "7"},
		{name: "collapses a run", in: []int{41, 42, 43, 44}, want: "41-44"},
		{name: "mixed", in: []int{3, 7, 8, 9, 20}, want: "3, 7-9, 20"},
		{
			name: "truncates many groups",
			in:   []int{1, 3, 5, 7, 9, 11, 13},
			want: "1, 3, 5, 7, 9, and 2 more",
		},
	}
	for _, tt := range tests {
		if got := summarizeSegments(tt.in); got != tt.want {
			t.Fatalf("%s: summarizeSegments(%v) = %q, want %q", tt.name, tt.in, got, tt.want)
		}
	}
}

func TestSetReporterFallsBackToHumanReporter(t *testing.T) {
	downloader := &Downloader{}
	downloader.SetReporter(nil)
	if _, ok := downloader.reporter.(*HumanReporter); !ok {
		t.Fatalf("reporter = %T, want *HumanReporter", downloader.reporter)
	}
}

func TestSetRemuxerFallsBackToFFmpegRemuxer(t *testing.T) {
	downloader := &Downloader{}
	downloader.SetRemuxer(nil)
	if _, ok := downloader.remuxer.(FFmpegRemuxer); !ok {
		t.Fatalf("remuxer = %T, want FFmpegRemuxer", downloader.remuxer)
	}
}

func TestSetDurationProberFallsBackToFFprobeDurationProber(t *testing.T) {
	downloader := &Downloader{}
	downloader.SetDurationProber(nil)
	if _, ok := downloader.prober.(FFprobeDurationProber); !ok {
		t.Fatalf("prober = %T, want FFprobeDurationProber", downloader.prober)
	}
}

func TestSetConversion(t *testing.T) {
	downloader := &Downloader{}
	downloader.SetConversion(true, false)
	if !downloader.convertToMP4 {
		t.Fatal("convertToMP4 = false, want true")
	}
	if downloader.keepTS {
		t.Fatal("keepTS = true, want false")
	}
}

func TestExpectedDuration(t *testing.T) {
	downloader := &Downloader{
		result: &parse.Result{
			M3u8: &parse.M3u8{
				Segments: []*parse.Segment{
					{Duration: 1.5},
					nil,
					{Duration: 2.25},
				},
			},
		},
	}
	if got := downloader.expectedDuration(); got != 3.75 {
		t.Fatalf("expectedDuration = %v, want 3.75", got)
	}
}
