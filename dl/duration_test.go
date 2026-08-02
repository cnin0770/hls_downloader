package dl

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestFFprobeDurationProberBuildsCommand(t *testing.T) {
	var gotName string
	var gotArgs []string
	prober := FFprobeDurationProber{
		LookPath: func(name string) (string, error) {
			if name != "ffprobe" {
				t.Fatalf("LookPath(%q), want ffprobe", name)
			}
			return "/usr/local/bin/ffprobe", nil
		},
		Output: func(name string, args ...string) ([]byte, error) {
			gotName = name
			gotArgs = append([]string(nil), args...)
			return []byte("12.345\n"), nil
		},
	}

	duration, err := prober.Duration("movie.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if duration != 12.345 {
		t.Fatalf("duration = %v, want 12.345", duration)
	}
	if gotName != "/usr/local/bin/ffprobe" {
		t.Fatalf("command name = %q, want /usr/local/bin/ffprobe", gotName)
	}
	wantArgs := []string{
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		"movie.mp4",
	}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestFFprobeDurationProberMissingFFprobe(t *testing.T) {
	prober := FFprobeDurationProber{
		LookPath: func(name string) (string, error) {
			return "", errors.New("not found")
		},
	}

	_, err := prober.Duration("movie.mp4")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), conversionToolMissing) {
		t.Fatalf("error = %q, want %q", err.Error(), conversionToolMissing)
	}
}

func TestFFprobeDurationProberRejectsInvalidDuration(t *testing.T) {
	prober := FFprobeDurationProber{
		Binary: "ffprobe",
		Output: func(name string, args ...string) ([]byte, error) {
			return []byte("not-a-duration"), nil
		},
	}

	_, err := prober.Duration("movie.mp4")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "parse ffprobe duration") {
		t.Fatalf("error = %q, want parse ffprobe duration", err.Error())
	}
}

func TestDurationTolerance(t *testing.T) {
	if got := durationTolerance(100); got != 5 {
		t.Fatalf("durationTolerance(100) = %v, want 5", got)
	}
	if got := durationTolerance(1000); got != 10 {
		t.Fatalf("durationTolerance(1000) = %v, want 10", got)
	}
}

func TestDurationSuspect(t *testing.T) {
	tests := []struct {
		name     string
		expected float64
		actual   float64
		want     bool
	}{
		{name: "within fixed tolerance", expected: 100, actual: 104.9, want: false},
		{name: "outside fixed tolerance", expected: 100, actual: 106, want: true},
		{name: "within percent tolerance", expected: 1000, actual: 1009, want: false},
		{name: "outside percent tolerance", expected: 1000, actual: 1011, want: true},
		{name: "unknown expected", expected: 0, actual: 100, want: false},
		{name: "unknown actual", expected: 100, actual: 0, want: false},
	}

	for _, tt := range tests {
		got := durationSuspect(tt.expected, tt.actual)
		if got != tt.want {
			t.Fatalf("%s: durationSuspect(%v, %v) = %v, want %v",
				tt.name, tt.expected, tt.actual, got, tt.want)
		}
	}
}

func TestUnexplainedShortfall(t *testing.T) {
	tests := []struct {
		name         string
		expected     float64
		actual       float64
		knownMissing float64
		want         float64
		significant  bool
	}{
		{
			// Failed segments account for the whole shortfall: nothing else is wrong.
			name: "fully explained by missing segments",
			expected: 7264, actual: 7228, knownMissing: 36,
			want: 0, significant: false,
		},
		{
			// Short by more than the failed segments explain.
			name: "extra loss beyond the failed segments",
			expected: 7264, actual: 7114, knownMissing: 36,
			want: 114, significant: true,
		},
		{
			// No segment failed, so the whole shortfall is unexplained. 20s is
			// inside the 1% tolerance of a 2-hour video, so it is not flagged.
			name: "small shortfall stays within tolerance",
			expected: 7264, actual: 7244, knownMissing: 0,
			want: 20, significant: false,
		},
		{
			name: "output longer than expected is negative",
			expected: 120, actual: 200, knownMissing: 0,
			want: -80, significant: true,
		},
		{
			name: "unknown durations are never significant",
			expected: 0, actual: 100, knownMissing: 0,
			want: 0, significant: false,
		},
	}
	for _, tt := range tests {
		got, significant := unexplainedShortfall(tt.expected, tt.actual, tt.knownMissing)
		if math.Abs(got-tt.want) > 0.001 || significant != tt.significant {
			t.Fatalf("%s: unexplainedShortfall = (%v, %v), want (%v, %v)",
				tt.name, got, significant, tt.want, tt.significant)
		}
	}
}

func TestSuspectMP4Filename(t *testing.T) {
	got := suspectMP4Filename("/tmp/movie.mp4", 3*3600+6*60+34)
	if got != "/tmp/movie.030634.mp4" {
		t.Fatalf("suspectMP4Filename = %q, want /tmp/movie.030634.mp4", got)
	}
}

func TestDurationTag(t *testing.T) {
	tests := []struct {
		name     string
		duration float64
		want     string
	}{
		{name: "hours minutes seconds", duration: 3*3600 + 6*60 + 34, want: "030634"},
		{name: "whole minute", duration: 3*3600 + 6*60, want: "030600"},
		{name: "rounds to nearest second", duration: 3*3600 + 6*60 + 34.6, want: "030635"},
	}

	for _, tt := range tests {
		got := durationTag(tt.duration)
		if got != tt.want {
			t.Fatalf("%s: durationTag(%v) = %q, want %q", tt.name, tt.duration, got, tt.want)
		}
	}
}
