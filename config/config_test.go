package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingConfigReturnsDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Concurrency != 25 {
		t.Fatalf("Concurrency = %d, want 25", cfg.Concurrency)
	}
	if !cfg.KeepTS {
		t.Fatal("KeepTS = false, want true")
	}
}

func TestLoadConfigOverlaysDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"downloadDir":"/tmp/video","convertToMp4":true,"ffmpegLog":true}`), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DownloadDir != "/tmp/video" {
		t.Fatalf("DownloadDir = %q, want /tmp/video", cfg.DownloadDir)
	}
	if !cfg.ConvertToMP4 {
		t.Fatal("ConvertToMP4 = false, want true")
	}
	if !cfg.FFmpegLog {
		t.Fatal("FFmpegLog = false, want true")
	}
	if cfg.Concurrency != 25 {
		t.Fatalf("Concurrency = %d, want default 25", cfg.Concurrency)
	}
	if !cfg.KeepTS {
		t.Fatal("KeepTS = false, want default true")
	}
}

func TestSaveConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	cfg := Default()
	cfg.DownloadDir = "/tmp/video"
	cfg.Concurrency = 8
	cfg.UserAgent = "m3u8-test"

	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.DownloadDir != cfg.DownloadDir || got.Concurrency != cfg.Concurrency || got.UserAgent != cfg.UserAgent {
		t.Fatalf("Load(Save(cfg)) = %+v, want %+v", got, cfg)
	}
}
