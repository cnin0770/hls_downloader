package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/cnin0770/m3u8_ui/config"
	"github.com/cnin0770/m3u8_ui/dl"
	"github.com/cnin0770/m3u8_ui/tool"
)

var (
	url          string
	output       string
	name         string
	configPath   string
	chanSize     int
	referer      string
	userAgent    string
	convertToMP4 bool
	keepTS       bool
	jsonOutput   bool
	ffmpegLog    bool
	printConfig  bool
)

func init() {
	defaults := config.Default()
	flag.StringVar(&url, "u", "", "M3U8 URL, required")
	flag.IntVar(&chanSize, "c", defaults.Concurrency, "Maximum number of occurrences")
	flag.StringVar(&output, "o", "", "Output folder (required unless set in config)")
	flag.StringVar(&name, "n", "", "Output filename without extension (optional)")
	flag.StringVar(&referer, "r", "", "Referer header to send with every request (optional)")
	flag.StringVar(&userAgent, "ua", "", "User-Agent header to send with every request (optional)")
	flag.BoolVar(&convertToMP4, "mp4", defaults.ConvertToMP4, "Convert merged TS to MP4 after download (optional)")
	flag.BoolVar(&keepTS, "keep-ts", defaults.KeepTS, "Keep merged TS file after conversion")
	flag.BoolVar(&jsonOutput, "json", defaults.JSONOutput, "Emit newline-delimited JSON events")
	flag.BoolVar(&ffmpegLog, "ffmpeg-log", defaults.FFmpegLog, "Show ffmpeg output during MP4 conversion")
	flag.BoolVar(&printConfig, "print-config", false, "Print effective config as JSON and exit")
	flag.StringVar(&configPath, "config", "", "Config file path (optional)")
}

func main() {
	flag.Parse()
	cfg, err := effectiveConfig()
	jsonMode := jsonOutput
	if err == nil {
		jsonMode = cfg.JSONOutput
	}
	if err == nil && printConfig {
		if err = printEffectiveConfig(os.Stdout, cfg); err != nil {
			jsonMode = false
		}
	}
	if err == nil && !printConfig {
		err = run(cfg)
	}
	if err != nil {
		writeError(os.Stdout, os.Stderr, jsonMode, err)
		os.Exit(1)
	}
}

func effectiveConfig() (config.Config, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return cfg, fmt.Errorf("load config: %s", err.Error())
	}
	return applyFlagOverrides(cfg, explicitFlags()), nil
}

func run(cfg config.Config) error {
	if url == "" {
		return parameterError("u")
	}
	if cfg.DownloadDir == "" {
		return parameterError("o")
	}
	if cfg.Concurrency <= 0 {
		return errors.New("parameter 'c' must be greater than 0")
	}
	tool.Referer = cfg.Referer
	tool.UserAgent = cfg.UserAgent
	downloader, err := dl.NewTask(cfg.DownloadDir, url, name)
	if err != nil {
		return err
	}
	if cfg.JSONOutput {
		downloader.SetReporter(dl.NewJSONReporter(os.Stdout))
	}
	if cfg.FFmpegLog {
		downloader.SetRemuxer(dl.FFmpegRemuxer{Verbose: true})
	}
	downloader.SetConversion(cfg.ConvertToMP4, cfg.KeepTS)
	if err := downloader.Start(cfg.Concurrency); err != nil {
		return err
	}
	if !cfg.JSONOutput {
		fmt.Println("Done!")
	}
	return nil
}

func printEffectiveConfig(writer io.Writer, cfg config.Config) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(cfg)
}

func writeError(stdout io.Writer, stderr io.Writer, jsonMode bool, err error) {
	if jsonMode {
		_ = json.NewEncoder(stdout).Encode(dl.Event{Type: dl.EventError, Message: err.Error()})
		return
	}
	fmt.Fprintln(stderr, "[error]", err)
}

func explicitFlags() map[string]bool {
	set := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) {
		set[f.Name] = true
	})
	return set
}

func applyFlagOverrides(cfg config.Config, set map[string]bool) config.Config {
	if set["o"] {
		cfg.DownloadDir = output
	}
	if set["c"] {
		cfg.Concurrency = chanSize
	}
	if set["r"] {
		cfg.Referer = referer
	}
	if set["ua"] {
		cfg.UserAgent = userAgent
	}
	if set["mp4"] {
		cfg.ConvertToMP4 = convertToMP4
	}
	if set["keep-ts"] {
		cfg.KeepTS = keepTS
	}
	if set["json"] {
		cfg.JSONOutput = jsonOutput
	}
	if set["ffmpeg-log"] {
		cfg.FFmpegLog = ffmpegLog
	}
	return cfg
}

func parameterError(name string) error {
	return fmt.Errorf("parameter '%s' is required", name)
}
