package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cnin0770/m3u8_ui/config"
	"github.com/cnin0770/m3u8_ui/dl"
)

func TestApplyFlagOverrides(t *testing.T) {
	cfg := config.Config{
		DownloadDir:  "/config/downloads",
		Concurrency:  12,
		Referer:      "https://config.example",
		UserAgent:    "config-agent",
		ConvertToMP4: true,
		KeepTS:       true,
		JSONOutput:   true,
		FFmpegLog:    true,
	}

	output = "/cli/downloads"
	chanSize = 4
	referer = "https://cli.example"
	userAgent = "cli-agent"
	convertToMP4 = false
	keepTS = false
	jsonOutput = false
	ffmpegLog = false

	got := applyFlagOverrides(cfg, map[string]bool{
		"o":          true,
		"c":          true,
		"r":          true,
		"ua":         true,
		"mp4":        true,
		"keep-ts":    true,
		"json":       true,
		"ffmpeg-log": true,
	})

	if got.DownloadDir != output {
		t.Fatalf("DownloadDir = %q, want %q", got.DownloadDir, output)
	}
	if got.Concurrency != chanSize {
		t.Fatalf("Concurrency = %d, want %d", got.Concurrency, chanSize)
	}
	if got.Referer != referer {
		t.Fatalf("Referer = %q, want %q", got.Referer, referer)
	}
	if got.UserAgent != userAgent {
		t.Fatalf("UserAgent = %q, want %q", got.UserAgent, userAgent)
	}
	if got.ConvertToMP4 || got.KeepTS || got.JSONOutput || got.FFmpegLog {
		t.Fatalf("bool overrides = mp4:%v keepTS:%v json:%v ffmpegLog:%v, want all false",
			got.ConvertToMP4, got.KeepTS, got.JSONOutput, got.FFmpegLog)
	}
}

func TestApplyFlagOverridesLeavesConfigWhenUnset(t *testing.T) {
	cfg := config.Config{
		DownloadDir: "/config/downloads",
		Concurrency: 12,
		Referer:     "https://config.example",
		UserAgent:   "config-agent",
		KeepTS:      true,
	}

	got := applyFlagOverrides(cfg, map[string]bool{})

	if got != cfg {
		t.Fatalf("applyFlagOverrides changed cfg with no flags: got %+v want %+v", got, cfg)
	}
}

func TestPrintEffectiveConfig(t *testing.T) {
	cfg := config.Default()
	cfg.DownloadDir = "/tmp/downloads"
	cfg.FFmpegLog = true

	var buf bytes.Buffer
	if err := printEffectiveConfig(&buf, cfg); err != nil {
		t.Fatal(err)
	}

	var got config.Config
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.DownloadDir != cfg.DownloadDir || !got.FFmpegLog {
		t.Fatalf("printed config = %+v, want %+v", got, cfg)
	}
}

func TestWriteErrorJSON(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	writeError(&stdout, &stderr, true, parameterError("u"))

	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var got dl.Event
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != dl.EventError || !strings.Contains(got.Message, "parameter 'u'") {
		t.Fatalf("event = %+v, want JSON error for missing u", got)
	}
}

func TestWriteErrorHuman(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	writeError(&stdout, &stderr, false, parameterError("o"))

	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "[error] parameter 'o' is required") {
		t.Fatalf("stderr = %q, want human error", stderr.String())
	}
}

func TestRunRequiresURL(t *testing.T) {
	oldURL := url
	defer func() {
		url = oldURL
	}()
	url = ""

	err := run(config.Config{DownloadDir: "/tmp/downloads", Concurrency: 1})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "parameter 'u' is required") {
		t.Fatalf("error = %q, want missing u", err.Error())
	}
}

func TestRunRequiresDownloadDir(t *testing.T) {
	oldURL := url
	defer func() {
		url = oldURL
	}()
	url = "https://example.com/index.m3u8"

	err := run(config.Config{Concurrency: 1})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "parameter 'o' is required") {
		t.Fatalf("error = %q, want missing o", err.Error())
	}
}

func TestRunRequiresPositiveConcurrency(t *testing.T) {
	oldURL := url
	defer func() {
		url = oldURL
	}()
	url = "https://example.com/index.m3u8"

	err := run(config.Config{DownloadDir: "/tmp/downloads", Concurrency: 0})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "parameter 'c' must be greater than 0") {
		t.Fatalf("error = %q, want invalid concurrency", err.Error())
	}
}
