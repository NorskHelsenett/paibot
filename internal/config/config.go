package config

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
		Chat   string `yaml:"chat"`
		Thread string `yaml:"thread"`
	} `yaml:"prompts"`
	Reactions struct {
		Thinking string `yaml:"thinking"`
		Success  string `yaml:"success"`
		Error    string `yaml:"error"`
		Fatal    string `yaml:"fatal"`
	} `yaml:"reactions"`
}

// Load reads and parses the YAML config file.
func Load(path string) (*BotConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	cfg := &BotConfig{
		Temp: 0.7, // default
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	cfg.Prompts.Chat = strings.TrimSpace(cfg.Prompts.Chat)
	cfg.Prompts.Thread = strings.TrimSpace(cfg.Prompts.Thread)

	if cfg.Reactions.Thinking == "" {
		cfg.Reactions.Thinking = "thinking_face"
	}
	if cfg.Reactions.Success == "" {
		cfg.Reactions.Success = "yes_check_mark_animated"
	}
	if cfg.Reactions.Error == "" {
		cfg.Reactions.Error = "usererror"
	}
	if cfg.Reactions.Fatal == "" {
		cfg.Reactions.Fatal = "skull"
	}

	if cfg.Model == "" {
		return nil, fmt.Errorf("config: model is required")
	}

	return cfg, nil
}
