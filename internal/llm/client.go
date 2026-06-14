package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	defaultTimeout         = 5 * time.Minute
	defaultMaxOutputTokens = 16000

	providerAnthropic = "anthropic"
	providerOpenAI    = "openai"

	envAuthHeader = "RECALL_LLM_AUTH_HEADER"
	envHeaders    = "RECALL_LLM_HEADERS"
)

// Client sends a prompt and JSON schema to an LLM provider and returns the
// provider's schema-constrained JSON text plus token usage.
type Client interface {
	GenerateStructured(ctx context.Context, req StructuredRequest) (Response, error)
}

// Response is a structured generation result.
type Response struct {
	Text  string
	Usage Usage
}

// Usage reports tokens billed for a single LLM call.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Add accumulates another call's usage, for summing across retry attempts.
func (u *Usage) Add(other Usage) {
	u.InputTokens += other.InputTokens
	u.OutputTokens += other.OutputTokens
}

type StructuredRequest struct {
	Model          string
	ReasoningLevel string
	SystemPrompt   string
	UserPrompt     string
	SchemaName     string
	Schema         map[string]any
}

func New(provider string) (Client, error) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case providerAnthropic:
		endpoint, err := endpointFromBase("ANTHROPIC_BASE_URL", anthropicDefaultBaseURL, anthropicMessagesPath)
		if err != nil {
			return nil, err
		}
		headers, err := requestHeaders("ANTHROPIC_API_KEY", "x-api-key", func(apiKey string) string {
			return apiKey
		}, map[string]string{
			"anthropic-version": anthropicVersion,
		})
		if err != nil {
			return nil, err
		}
		return newAnthropicClient(endpoint, headers, defaultHTTPClient()), nil
	case providerOpenAI:
		endpoint, err := endpointFromBase("OPENAI_BASE_URL", openAIDefaultBaseURL, openAIResponsesPath)
		if err != nil {
			return nil, err
		}
		headers, err := requestHeaders("OPENAI_API_KEY", "authorization", func(apiKey string) string {
			return "Bearer " + apiKey
		}, nil)
		if err != nil {
			return nil, err
		}
		return newOpenAIClient(endpoint, headers, defaultHTTPClient()), nil
	default:
		return nil, fmt.Errorf("llm: unsupported provider %q", provider)
	}
}

func validateStructuredRequest(req StructuredRequest) error {
	if strings.TrimSpace(req.Model) == "" {
		return errors.New("llm: model is required")
	}
	if strings.TrimSpace(req.UserPrompt) == "" {
		return errors.New("llm: user prompt is required")
	}
	if strings.TrimSpace(req.SchemaName) == "" {
		return errors.New("llm: schema name is required")
	}
	if len(req.Schema) == 0 {
		return errors.New("llm: schema is required")
	}
	return nil
}

func normalizedReasoningLevel(level string) string {
	return strings.ToLower(strings.TrimSpace(level))
}

func reasoningEnabled(level string) bool {
	switch normalizedReasoningLevel(level) {
	case "", "off":
		return false
	default:
		return true
	}
}

func defaultHTTPClient() *http.Client {
	return &http.Client{Timeout: defaultTimeout}
}

func endpointFromBase(envName string, defaultBase string, path string) (string, error) {
	base := strings.TrimSpace(os.Getenv(envName))
	if base == "" {
		base = defaultBase
	}

	endpoint := strings.TrimRight(base, "/")
	if !strings.HasSuffix(endpoint, path) {
		endpoint += path
	}

	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("llm: invalid %s: %w", envName, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("llm: invalid %s: expected absolute URL", envName)
	}
	return endpoint, nil
}

func requestHeaders(apiKeyEnv string, authHeaderName string, authHeaderValue func(string) string, baseHeaders map[string]string) (map[string]string, error) {
	headers := map[string]string{}
	for key, value := range baseHeaders {
		headers[key] = value
	}

	authHeader := strings.TrimSpace(os.Getenv(envAuthHeader))
	if authHeader != "" {
		key, value, err := parseHeader(authHeader)
		if err != nil {
			return nil, err
		}
		headers[key] = value
	} else {
		apiKey := strings.TrimSpace(os.Getenv(apiKeyEnv))
		if apiKey == "" {
			return nil, fmt.Errorf("llm: %s is not set", apiKeyEnv)
		}
		headers[authHeaderName] = authHeaderValue(apiKey)
	}

	extraHeaders, err := extraRequestHeaders()
	if err != nil {
		return nil, err
	}
	for key, value := range extraHeaders {
		headers[key] = value
	}

	return headers, nil
}

func parseHeader(header string) (string, string, error) {
	key, value, ok := strings.Cut(header, ":")
	if !ok {
		return "", "", fmt.Errorf("llm: %s must use \"Header-Name: value\" format", envAuthHeader)
	}
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return "", "", fmt.Errorf("llm: %s must include a non-empty header name and value", envAuthHeader)
	}
	return key, value, nil
}

func extraRequestHeaders() (map[string]string, error) {
	raw := strings.TrimSpace(os.Getenv(envHeaders))
	if raw == "" {
		return nil, nil
	}

	headers := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &headers); err != nil {
		return nil, fmt.Errorf("llm: parsing %s: %w", envHeaders, err)
	}
	for key, value := range headers {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("llm: %s must contain non-empty header names and values", envHeaders)
		}
	}
	return headers, nil
}
