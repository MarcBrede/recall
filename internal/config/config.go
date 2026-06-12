package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	DirName               = ".recall"
	FileName              = "config.json"
	DefaultProvider       = "anthropic"
	DefaultModel          = "claude-opus-4-8"
	DefaultReasoningLevel = "high"
	DefaultConcurrency    = 10
)

type Loaded struct {
	Config Config
	Dir    string
	Path   string
}

type Config struct {
	ConfigVersion int          `json:"config_version"`
	LLM           LLMConfig    `json:"llm"`
	Ingest        IngestConfig `json:"ingest"`
}

type LLMConfig struct {
	Provider  string          `json:"provider"`
	Model     string          `json:"model"`
	Reasoning ReasoningConfig `json:"reasoning"`
}

type ReasoningConfig struct {
	Level string `json:"level"`
}

type IngestConfig struct {
	Concurrency int `json:"concurrency"`
}

func Load(_ string) (Loaded, error) {
	dir, err := homeRecallDir()
	if err != nil {
		return Loaded{}, err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return Loaded{}, err
	}

	path := filepath.Join(dir, FileName)
	if err := ensureFile(path); err != nil {
		return Loaded{}, err
	}

	config, err := readFile(path)
	if err != nil {
		return Loaded{}, err
	}

	return Loaded{
		Config: config,
		Dir:    dir,
		Path:   path,
	}, nil
}

func Default() Config {
	return Config{
		ConfigVersion: 1,
		LLM: LLMConfig{
			Provider: DefaultProvider,
			Model:    DefaultModel,
			Reasoning: ReasoningConfig{
				Level: DefaultReasoningLevel,
			},
		},
		Ingest: IngestConfig{
			Concurrency: DefaultConcurrency,
		},
	}
}

func (config Config) ValidateLLM(path string) error {
	var missing []string
	if strings.TrimSpace(config.LLM.Provider) == "" {
		missing = append(missing, "llm.provider")
	}
	if strings.TrimSpace(config.LLM.Model) == "" {
		missing = append(missing, "llm.model")
	}
	if len(missing) > 0 {
		return fmt.Errorf("config: set %s in %s", strings.Join(missing, " and "), path)
	}
	return nil
}

func (config Config) ValidateIngest(path string) error {
	if config.Ingest.Concurrency <= 0 {
		return fmt.Errorf("config: ingest.concurrency must be > 0 in %s", path)
	}
	return nil
}

func homeRecallDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(home) == "" {
		return "", errors.New("config: user home directory is empty")
	}
	return filepath.Join(home, DirName), nil
}

func ensureFile(path string) error {
	_, err := os.Stat(path)
	if err == nil {
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	data, err := json.MarshalIndent(Default(), "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

func readFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}

	config := Default()
	if err := json.Unmarshal(data, &config); err != nil {
		return Config{}, fmt.Errorf("config: parse %s: %w", path, err)
	}
	config.LLM.Provider = strings.TrimSpace(config.LLM.Provider)
	config.LLM.Model = strings.TrimSpace(config.LLM.Model)
	config.LLM.Reasoning.Level = strings.TrimSpace(config.LLM.Reasoning.Level)
	if config.LLM.Reasoning.Level == "" {
		config.LLM.Reasoning.Level = "off"
	}
	return config, nil
}
