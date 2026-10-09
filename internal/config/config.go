package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Location struct {
	Latitude     float64 `yaml:"latitude"`
	Longitude    float64 `yaml:"longitude"`
	DeviceModel  string  `yaml:"device_model"`
	DeviceSystem string  `yaml:"device_system"`
	Address      string  `yaml:"address"`
	City         string  `yaml:"city"`
	Road         string  `yaml:"road"`
	Poi          string  `yaml:"poi"`
}

type Schedule struct {
	PrimaryMinuteOffset int   `yaml:"primary_minute_offset"`
	PrimaryJitterSec    int   `yaml:"primary_jitter_sec"`
	RetryMinuteOffsets  []int `yaml:"retry_minute_offsets"`
}

type Log struct {
	File  string `yaml:"file"`
	Level string `yaml:"level"`
}

type Config struct {
	Token    string   `yaml:"token"`
	RuleID   int      `yaml:"rule_id"`
	Location Location `yaml:"location"`
	Schedule Schedule `yaml:"schedule"`
	Log      Log      `yaml:"log"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	c.applyDefaults()
	return &c, nil
}

func (c *Config) validate() error {
	if c.Token == "" {
		return fmt.Errorf("config.token is empty")
	}
	if c.RuleID == 0 {
		return fmt.Errorf("config.rule_id is 0")
	}
	if c.Location.Latitude == 0 || c.Location.Longitude == 0 {
		return fmt.Errorf("config.location.latitude/longitude is 0; fill real coords")
	}
	if c.Location.Address == "" {
		return fmt.Errorf("config.location.address is empty; the current signing protocol requires it")
	}
	return nil
}

func (c *Config) applyDefaults() {
	if c.Location.DeviceModel == "" {
		c.Location.DeviceModel = "Pixel 7"
	}
	if c.Location.DeviceSystem == "" {
		c.Location.DeviceSystem = "Android 13"
	}
	if c.Schedule.PrimaryMinuteOffset == 0 {
		c.Schedule.PrimaryMinuteOffset = 2
	}
	if c.Schedule.PrimaryJitterSec == 0 {
		c.Schedule.PrimaryJitterSec = 180
	}
	if len(c.Schedule.RetryMinuteOffsets) == 0 {
		c.Schedule.RetryMinuteOffsets = []int{8, 15, 22}
	}
	if c.Log.File == "" {
		c.Log.File = "wangui.log"
	}
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
}

// MaskToken returns a redacted form for logging: first 8 + "..." + last 4.
func MaskToken(tok string) string {
	if len(tok) <= 14 {
		return "***"
	}
	return tok[:8] + "..." + tok[len(tok)-4:]
}
