package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCreatesRecallDirAndDefaultConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	loaded, err := Load("/ignored/project")
	if err != nil {
		t.Fatal(err)
	}

	if want := filepath.Join(home, DirName); loaded.Dir != want {
		t.Fatalf("dir = %q, want %q", loaded.Dir, want)
	}
	if _, err := os.Stat(filepath.Join(home, DirName, FileName)); err != nil {
		t.Fatal(err)
	}
	if loaded.Config.ConfigVersion != 1 {
		t.Fatalf("config_version = %d, want 1", loaded.Config.ConfigVersion)
	}
	if loaded.Config.LLM.Provider != DefaultProvider {
		t.Fatalf("provider = %q, want %q", loaded.Config.LLM.Provider, DefaultProvider)
	}
	if loaded.Config.LLM.Model != DefaultModel {
		t.Fatalf("model = %q, want %q", loaded.Config.LLM.Model, DefaultModel)
	}
	if loaded.Config.LLM.Reasoning.Level != DefaultReasoningLevel {
		t.Fatalf("reasoning level = %q, want %q", loaded.Config.LLM.Reasoning.Level, DefaultReasoningLevel)
	}
	if loaded.Config.Ingest.Concurrency != DefaultConcurrency {
		t.Fatalf("ingest concurrency = %d, want %d", loaded.Config.Ingest.Concurrency, DefaultConcurrency)
	}
}

func TestLoadUsesHomeRecallDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	recallDir := filepath.Join(home, DirName)
	if err := os.Mkdir(recallDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := writeConfig(filepath.Join(recallDir, FileName), Config{
		ConfigVersion: 1,
		LLM: LLMConfig{
			Provider: "anthropic",
			Model:    "claude-test",
			Reasoning: ReasoningConfig{
				Level: "low",
			},
		},
		Ingest: IngestConfig{
			Concurrency: 22,
		},
	}); err != nil {
		t.Fatal(err)
	}

	child := filepath.Join(t.TempDir(), "a", "b")
	if err := os.MkdirAll(child, 0755); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(child)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Dir != recallDir {
		t.Fatalf("dir = %q, want %q", loaded.Dir, recallDir)
	}
	if got, want := loaded.Config.LLM.Model, "claude-test"; got != want {
		t.Fatalf("model = %q, want %q", got, want)
	}
	if got, want := loaded.Config.Ingest.Concurrency, 22; got != want {
		t.Fatalf("ingest concurrency = %d, want %d", got, want)
	}
}

func TestValidateLLMRequiresProviderAndModel(t *testing.T) {
	config := Default()
	config.LLM.Provider = ""
	config.LLM.Model = ""

	err := config.ValidateLLM("/tmp/config.json")
	if err == nil {
		t.Fatal("err is nil")
	}
}

func TestValidateIngestRequiresPositiveConcurrency(t *testing.T) {
	config := Default()
	config.Ingest.Concurrency = 0

	err := config.ValidateIngest("/tmp/config.json")
	if err == nil {
		t.Fatal("err is nil")
	}
}

func writeConfig(path string, config Config) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}
