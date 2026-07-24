package dl

import (
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

func TestGenSlice(t *testing.T) {
	got := genSlice(4)
	want := []int{0, 1, 2, 3}
	if len(got) != len(want) {
		t.Fatalf("len(genSlice(4)) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("genSlice(4)[%d] = %d, want %d", i, got[i], want[i])
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
