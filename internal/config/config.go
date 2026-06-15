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
	DefaultSearchProvider = "openai"
	DefaultSearchModel    = "text-embedding-3-small"
	DefaultSearchEnabled  = false
	DefaultAuthType       = "provider_env"

	AuthProviderEnv   = "provider_env"
	AuthHeaderEnv     = "header_env"
	AuthHeaderCommand = "header_command"
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
	Search        SearchConfig `json:"search"`
}

type LLMConfig struct {
	Provider  string            `json:"provider"`
	Model     string            `json:"model"`
	Reasoning ReasoningConfig   `json:"reasoning"`
	BaseURL   string            `json:"base_url,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Auth      LLMAuthConfig     `json:"auth,omitempty"`
}

type ReasoningConfig struct {
	Level string `json:"level"`
}

type LLMAuthConfig struct {
	Type    string   `json:"type,omitempty"`
	Env     string   `json:"env,omitempty"`
	Command []string `json:"command,omitempty"`
}

type IngestConfig struct {
	Concurrency int `json:"concurrency"`
}

type SearchConfig struct {
	Enabled  bool              `json:"enabled"`
	Provider string            `json:"provider"`
	Model    string            `json:"model"`
	BaseURL  string            `json:"base_url,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Auth     LLMAuthConfig     `json:"auth,omitempty"`
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
			Auth: LLMAuthConfig{
				Type: DefaultAuthType,
			},
		},
		Ingest: IngestConfig{
			Concurrency: DefaultConcurrency,
		},
		Search: SearchConfig{
			Enabled:  DefaultSearchEnabled,
			Provider: DefaultSearchProvider,
			Model:    DefaultSearchModel,
			Auth: LLMAuthConfig{
				Type: DefaultAuthType,
			},
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
	for key, value := range config.LLM.Headers {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			return fmt.Errorf("config: llm.headers must contain non-empty header names and values in %s", path)
		}
	}
	switch strings.TrimSpace(config.LLM.Auth.Type) {
	case "", AuthProviderEnv:
	case AuthHeaderEnv:
		if strings.TrimSpace(config.LLM.Auth.Env) == "" {
			return fmt.Errorf("config: llm.auth.env is required when llm.auth.type is %q in %s", AuthHeaderEnv, path)
		}
	case AuthHeaderCommand:
		if len(config.LLM.Auth.Command) == 0 || strings.TrimSpace(config.LLM.Auth.Command[0]) == "" {
			return fmt.Errorf("config: llm.auth.command is required when llm.auth.type is %q in %s", AuthHeaderCommand, path)
		}
	default:
		return fmt.Errorf("config: unsupported llm.auth.type %q in %s", config.LLM.Auth.Type, path)
	}
	return nil
}

func (config Config) ValidateIngest(path string) error {
	if config.Ingest.Concurrency <= 0 {
		return fmt.Errorf("config: ingest.concurrency must be > 0 in %s", path)
	}
	return nil
}

func (config Config) ValidateSearch(path string) error {
	if !config.Search.Enabled {
		return nil
	}
	var missing []string
	if strings.TrimSpace(config.Search.Provider) == "" {
		missing = append(missing, "search.provider")
	}
	if strings.TrimSpace(config.Search.Model) == "" {
		missing = append(missing, "search.model")
	}
	if len(missing) > 0 {
		return fmt.Errorf("config: set %s in %s", strings.Join(missing, " and "), path)
	}
	for key, value := range config.Search.Headers {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			return fmt.Errorf("config: search.headers must contain non-empty header names and values in %s", path)
		}
	}
	switch strings.TrimSpace(config.Search.Auth.Type) {
	case "", AuthProviderEnv:
	case AuthHeaderEnv:
		if strings.TrimSpace(config.Search.Auth.Env) == "" {
			return fmt.Errorf("config: search.auth.env is required when search.auth.type is %q in %s", AuthHeaderEnv, path)
		}
	case AuthHeaderCommand:
		if len(config.Search.Auth.Command) == 0 || strings.TrimSpace(config.Search.Auth.Command[0]) == "" {
			return fmt.Errorf("config: search.auth.command is required when search.auth.type is %q in %s", AuthHeaderCommand, path)
		}
	default:
		return fmt.Errorf("config: unsupported search.auth.type %q in %s", config.Search.Auth.Type, path)
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
	config.LLM.BaseURL = strings.TrimSpace(config.LLM.BaseURL)
	config.LLM.Reasoning.Level = strings.TrimSpace(config.LLM.Reasoning.Level)
	if config.LLM.Reasoning.Level == "" {
		config.LLM.Reasoning.Level = "off"
	}
	config.LLM.Auth.Type = strings.TrimSpace(config.LLM.Auth.Type)
	if config.LLM.Auth.Type == "" {
		config.LLM.Auth.Type = DefaultAuthType
	}
	config.LLM.Auth.Env = strings.TrimSpace(config.LLM.Auth.Env)
	config.LLM.Auth.Command = compactStrings(config.LLM.Auth.Command)
	config.LLM.Headers = compactHeaders(config.LLM.Headers)
	config.Search.Provider = strings.TrimSpace(config.Search.Provider)
	if config.Search.Provider == "" {
		config.Search.Provider = DefaultSearchProvider
	}
	config.Search.Model = strings.TrimSpace(config.Search.Model)
	if config.Search.Model == "" {
		config.Search.Model = DefaultSearchModel
	}
	config.Search.BaseURL = strings.TrimSpace(config.Search.BaseURL)
	config.Search.Headers = compactHeaders(config.Search.Headers)
	config.Search.Auth.Type = strings.TrimSpace(config.Search.Auth.Type)
	if config.Search.Auth.Type == "" {
		config.Search.Auth.Type = DefaultAuthType
	}
	config.Search.Auth.Env = strings.TrimSpace(config.Search.Auth.Env)
	config.Search.Auth.Command = compactStrings(config.Search.Auth.Command)
	return config, nil
}

func compactStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		trimmed = append(trimmed, strings.TrimSpace(value))
	}
	return trimmed
}

func compactHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	trimmed := make(map[string]string, len(headers))
	for key, value := range headers {
		trimmed[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return trimmed
}
