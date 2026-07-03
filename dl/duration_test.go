package dl

import (
	"errors"
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
	if !strings.Contains(err.Error(), "ffprobe not found") {
		t.Fatalf("error = %q, want ffprobe not found", err.Error())
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

func TestSuspectMP4Filename(t *testing.T) {
	got := suspectMP4Filename("/tmp/movie.mp4")
	if got != "/tmp/movie.suspect.mp4" {
		t.Fatalf("suspectMP4Filename = %q, want /tmp/movie.suspect.mp4", got)
	}
}
