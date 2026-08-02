package dl

import (
	"bytes"
	"fmt"
	"math"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	durationToleranceSeconds = 5.0
	durationToleranceRatio   = 0.01
	conversionToolMissing    = "MP4 conversion requires local ffmpeg. Please install it and try again."
)

type DurationProber interface {
	Duration(path string) (float64, error)
}

type commandOutputRunner func(name string, args ...string) ([]byte, error)

type FFprobeDurationProber struct {
	Binary   string
	LookPath func(string) (string, error)
	Output   commandOutputRunner
}

func (p FFprobeDurationProber) Duration(path string) (float64, error) {
	binary, err := p.binary()
	if err != nil {
		return 0, err
	}
	output := p.Output
	if output == nil {
		output = runCommandOutput
	}
	bytes, err := output(binary, ffprobeDurationArgs(path)...)
	if err != nil {
		return 0, err
	}
	value := strings.TrimSpace(string(bytes))
	duration, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("parse ffprobe duration %q: %s", value, err.Error())
	}
	return duration, nil
}

func (p FFprobeDurationProber) binary() (string, error) {
	if p.Binary != "" {
		return p.Binary, nil
	}
	lookPath := p.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	binary, err := lookPath("ffprobe")
	if err != nil {
		return "", fmt.Errorf(conversionToolMissing)
	}
	return binary, nil
}

func runCommandOutput(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return nil, fmt.Errorf("%s: %s", err.Error(), message)
		}
		return nil, err
	}
	return output, nil
}

func ffprobeDurationArgs(path string) []string {
	return []string{
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	}
}

func durationTolerance(expected float64) float64 {
	ratioTolerance := math.Abs(expected) * durationToleranceRatio
	if ratioTolerance > durationToleranceSeconds {
		return ratioTolerance
	}
	return durationToleranceSeconds
}

func durationSuspect(expected float64, actual float64) bool {
	if expected <= 0 || actual <= 0 {
		return false
	}
	return math.Abs(expected-actual) > durationTolerance(expected)
}

// unexplainedShortfall reports how much of the duration gap is *not* accounted
// for by segments already known to be missing. Failed segments explain their own
// shortfall exactly, so what remains is content lost some other way — a
// truncated response that still returned 200, or a bad remux. The result is
// significant only beyond the usual tolerance, since EXTINF values are
// approximate and the residual is never exactly zero.
func unexplainedShortfall(expected float64, actual float64, knownMissing float64) (float64, bool) {
	if expected <= 0 || actual <= 0 {
		return 0, false
	}
	residual := (expected - actual) - knownMissing
	return residual, math.Abs(residual) > durationTolerance(expected)
}

func suspectMP4Filename(mp4Path string, expected float64) string {
	suffix := "." + durationTag(expected)
	ext := filepath.Ext(mp4Path)
	if strings.EqualFold(ext, ".mp4") {
		return strings.TrimSuffix(mp4Path, ext) + suffix + ".mp4"
	}
	return mp4Path + suffix
}

func durationTag(duration float64) string {
	totalSeconds := int(math.Round(duration))
	if totalSeconds < 0 {
		totalSeconds = 0
	}
	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60
	seconds := totalSeconds % 60
	return fmt.Sprintf("%02d%02d%02d", hours, minutes, seconds)
}
