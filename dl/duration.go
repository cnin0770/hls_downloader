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
	suspectSuffix            = ".suspect"
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
		return "", fmt.Errorf("ffprobe not found; install it and try again")
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

func suspectMP4Filename(mp4Path string) string {
	ext := filepath.Ext(mp4Path)
	if strings.EqualFold(ext, ".mp4") {
		return strings.TrimSuffix(mp4Path, ext) + suspectSuffix + ".mp4"
	}
	return mp4Path + suspectSuffix
}
