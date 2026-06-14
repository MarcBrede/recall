package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	defaultTimeout         = 5 * time.Minute
	defaultMaxOutputTokens = 120000

	providerAnthropic = "anthropic"
	providerOpenAI    = "openai"

	envAuthHeader = "RECALL_LLM_AUTH_HEADER"
	envHeaders    = "RECALL_LLM_HEADERS"

	AuthProviderEnv   = "provider_env"
	AuthHeaderEnv     = "header_env"
	AuthHeaderCommand = "header_command"
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

// Limiter caps concurrent LLM calls across an ingest run.
type Limiter struct {
	sem chan struct{}
}

func NewLimiter(concurrency int) *Limiter {
	if concurrency <= 0 {
		concurrency = 1
	}
	return &Limiter{sem: make(chan struct{}, concurrency)}
}

func (limiter *Limiter) Do(ctx context.Context, fn func(context.Context) (Response, error)) (Response, error) {
	if limiter == nil {
		return fn(ctx)
	}
	select {
	case limiter.sem <- struct{}{}:
		defer func() { <-limiter.sem }()
		return fn(ctx)
	case <-ctx.Done():
		return Response{}, ctx.Err()
	}
}

type StructuredRequest struct {
	Model          string
	ReasoningLevel string
	SystemPrompt   string
	UserPrompt     string
	SchemaName     string
	Schema         map[string]any
}

type Options struct {
	BaseURL string
	Headers map[string]string
	Auth    AuthConfig
}

type AuthConfig struct {
	Type    string
	Env     string
	Command []string
}

func New(provider string) (Client, error) {
	return NewWithOptions(provider, Options{})
}

func NewWithOptions(provider string, options Options) (Client, error) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case providerAnthropic:
		endpoint, err := endpointFromBaseValue(options.BaseURL, "ANTHROPIC_BASE_URL", anthropicDefaultBaseURL, anthropicMessagesPath)
		if err != nil {
			return nil, err
		}
		headers, err := requestHeadersWithOptions("ANTHROPIC_API_KEY", "x-api-key", func(apiKey string) string {
			return apiKey
		}, map[string]string{
			"anthropic-version": anthropicVersion,
		}, options)
		if err != nil {
			return nil, err
		}
		return newAnthropicClient(endpoint, headers, defaultHTTPClient()), nil
	case providerOpenAI:
		endpoint, err := endpointFromBaseValue(options.BaseURL, "OPENAI_BASE_URL", openAIDefaultBaseURL, openAIResponsesPath)
		if err != nil {
			return nil, err
		}
		headers, err := requestHeadersWithOptions("OPENAI_API_KEY", "authorization", func(apiKey string) string {
			return "Bearer " + apiKey
		}, nil, options)
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
	return endpointFromBaseValue("", envName, defaultBase, path)
}

func endpointFromBaseValue(configBase string, envName string, defaultBase string, path string) (string, error) {
	baseSource := envName
	base := strings.TrimSpace(configBase)
	if base == "" {
		base = strings.TrimSpace(os.Getenv(envName))
		if base == "" {
			base = defaultBase
		}
	} else {
		baseSource = "llm.base_url"
	}

	endpoint := strings.TrimRight(base, "/")
	if !strings.HasSuffix(endpoint, path) {
		endpoint += path
	}

	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("llm: invalid %s: %w", baseSource, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("llm: invalid %s: expected absolute URL", baseSource)
	}
	return endpoint, nil
}

func requestHeaders(apiKeyEnv string, authHeaderName string, authHeaderValue func(string) string, baseHeaders map[string]string) (map[string]string, error) {
	return requestHeadersWithOptions(apiKeyEnv, authHeaderName, authHeaderValue, baseHeaders, Options{})
}

func requestHeadersWithOptions(apiKeyEnv string, authHeaderName string, authHeaderValue func(string) string, baseHeaders map[string]string, options Options) (map[string]string, error) {
	headers := map[string]string{}
	for key, value := range baseHeaders {
		headers[key] = value
	}
	if err := mergeHeaders(headers, options.Headers, "llm.headers"); err != nil {
		return nil, err
	}

	key, value, err := authHeader(apiKeyEnv, authHeaderName, authHeaderValue, options.Auth)
	if err != nil {
		return nil, err
	}
	headers[key] = value

	extraHeaders, err := extraRequestHeaders()
	if err != nil {
		return nil, err
	}
	if err := mergeHeaders(headers, extraHeaders, envHeaders); err != nil {
		return nil, err
	}

	return headers, nil
}

func authHeader(apiKeyEnv string, authHeaderName string, authHeaderValue func(string) string, auth AuthConfig) (string, string, error) {
	envOverride := strings.TrimSpace(os.Getenv(envAuthHeader))
	if envOverride != "" {
		return parseHeaderNamed(envAuthHeader, envOverride)
	}

	switch strings.TrimSpace(auth.Type) {
	case "", AuthProviderEnv:
		apiKey := strings.TrimSpace(os.Getenv(apiKeyEnv))
		if apiKey == "" {
			return "", "", fmt.Errorf("llm: %s is not set", apiKeyEnv)
		}
		return authHeaderName, authHeaderValue(apiKey), nil
	case AuthHeaderEnv:
		envName := strings.TrimSpace(auth.Env)
		if envName == "" {
			return "", "", errors.New("llm: auth env is required for header_env")
		}
		header := strings.TrimSpace(os.Getenv(envName))
		if header == "" {
			return "", "", fmt.Errorf("llm: %s is not set", envName)
		}
		return parseHeaderNamed(envName, header)
	case AuthHeaderCommand:
		return authHeaderFromCommand(auth.Command)
	default:
		return "", "", fmt.Errorf("llm: unsupported auth type %q", auth.Type)
	}
}

func authHeaderFromCommand(args []string) (string, string, error) {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return "", "", errors.New("llm: auth command is required for header_command")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	command := exec.CommandContext(ctx, args[0], args[1:]...)
	output, err := command.Output()
	if ctx.Err() != nil {
		return "", "", fmt.Errorf("llm: auth command timed out: %w", ctx.Err())
	}
	if err != nil {
		return "", "", fmt.Errorf("llm: auth command failed: %w", err)
	}
	return parseHeaderNamed("llm.auth.command output", string(output))
}

func mergeHeaders(headers map[string]string, extra map[string]string, source string) error {
	for key, value := range extra {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			return fmt.Errorf("llm: %s must contain non-empty header names and values", source)
		}
		headers[key] = value
	}
	return nil
}

func parseHeader(header string) (string, string, error) {
	return parseHeaderNamed(envAuthHeader, header)
}

func parseHeaderNamed(source string, header string) (string, string, error) {
	key, value, ok := strings.Cut(header, ":")
	if !ok {
		return "", "", fmt.Errorf("llm: %s must use \"Header-Name: value\" format", source)
	}
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return "", "", fmt.Errorf("llm: %s must include a non-empty header name and value", source)
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
