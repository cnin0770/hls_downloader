package dl

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Remuxer interface {
	RemuxTS(input string, output string) error
}

type commandRunner func(name string, args ...string) error

type FFmpegRemuxer struct {
	Binary   string
	LookPath func(string) (string, error)
	Run      commandRunner
	Verbose  bool
}

func (r FFmpegRemuxer) RemuxTS(input string, output string) error {
	binary, err := r.binary()
	if err != nil {
		return err
	}
	run := r.Run
	if run == nil {
		run = runCommandQuiet
		if r.Verbose {
			run = runCommandVerbose
		}
	}
	return run(binary, ffmpegRemuxArgs(input, output)...)
}

func (r FFmpegRemuxer) binary() (string, error) {
	if r.Binary != "" {
		return r.Binary, nil
	}
	lookPath := r.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	binary, err := lookPath("ffmpeg")
	if err != nil {
		return "", fmt.Errorf("ffmpeg not found; install it and try again")
	}
	return binary, nil
}

func runCommandQuiet(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = io.Discard
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return fmt.Errorf("%s: %s", err.Error(), message)
		}
		return err
	}
	return nil
}

func runCommandVerbose(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func ffmpegRemuxArgs(input string, output string) []string {
	return []string{"-y", "-i", input, "-c", "copy", "-movflags", "+faststart", output}
}

func mp4Filename(tsPath string) string {
	ext := filepath.Ext(tsPath)
	if strings.EqualFold(ext, tsExt) {
		return strings.TrimSuffix(tsPath, ext) + ".mp4"
	}
	return tsPath + ".mp4"
}
