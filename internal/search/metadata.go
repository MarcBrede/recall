package search

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const MetadataFileName = "search.metadata.json"

type Metadata struct {
	SchemaVersion  int    `json:"schema_version"`
	EmbeddingModel string `json:"embedding_model"`
	EmbeddingDim   int    `json:"embedding_dim"`
	BuiltAt        string `json:"built_at"`
}

func loadMetadata(recallDir string) (Metadata, error) {
	data, err := os.ReadFile(MetadataPath(recallDir))
	if errors.Is(err, os.ErrNotExist) {
		return Metadata{}, nil
	}
	if err != nil {
		return Metadata{}, err
	}
	if strings.TrimSpace(string(data)) == "" {
		return Metadata{}, nil
	}
	var metadata Metadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return Metadata{}, err
	}
	return metadata, nil
}

func saveMetadata(recallDir string, metadata Metadata) error {
	if metadata.SchemaVersion == 0 {
		metadata.SchemaVersion = 1
	}
	if strings.TrimSpace(metadata.BuiltAt) == "" {
		metadata.BuiltAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if err := os.MkdirAll(recallDir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(MetadataPath(recallDir), data, 0644)
}

func MetadataPath(recallDir string) string {
	return filepath.Join(recallDir, MetadataFileName)
}
