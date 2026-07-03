package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const appSupportFolder = "M3U8"

type Config struct {
	DownloadDir  string `json:"downloadDir"`
	Concurrency  int    `json:"concurrency"`
	Referer      string `json:"referer"`
	UserAgent    string `json:"userAgent"`
	ConvertToMP4 bool   `json:"convertToMp4"`
	KeepTS       bool   `json:"keepTs"`
	JSONOutput   bool   `json:"jsonOutput"`
	FFmpegLog    bool   `json:"ffmpegLog"`
}

func Default() Config {
	return Config{
		Concurrency: 25,
		KeepTS:      true,
	}
}

func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, appSupportFolder, "config.json"), nil
}

func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return cfg, err
		}
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := json.Unmarshal(bytes, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func Save(path string, cfg Config) error {
	if path == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), os.ModePerm); err != nil {
		return err
	}
	bytes, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	bytes = append(bytes, '\n')
	return os.WriteFile(path, bytes, 0644)
}
