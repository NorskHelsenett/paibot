package main

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// BotConfig holds all non-secret configuration loaded from config.yaml.
type BotConfig struct {
	Model     string  `yaml:"model"`
	Temp      float32 `yaml:"temperature"`
	MaxTokens int     `yaml:"max_tokens"`
	Prompts   struct {
		Chat      string `yaml:"chat"`
		Thread    string `yaml:"thread"`
		Summarize string `yaml:"summarize"`
	} `yaml:"prompts"`
}

// LoadConfig reads and parses the YAML config file.
func LoadConfig(path string) (*BotConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	cfg := &BotConfig{
		// defaults
		Temp: 0.7,
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	cfg.Prompts.Chat = strings.TrimSpace(cfg.Prompts.Chat)
	cfg.Prompts.Thread = strings.TrimSpace(cfg.Prompts.Thread)
	cfg.Prompts.Summarize = strings.TrimSpace(cfg.Prompts.Summarize)

	if cfg.Model == "" {
		return nil, fmt.Errorf("config: model is required")
	}

	return cfg, nil
}
