package embed

import (
	"context"
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
	defaultTimeout = 2 * time.Minute

	ProviderOpenAI = "openai"

	AuthProviderEnv   = "provider_env"
	AuthHeaderEnv     = "header_env"
	AuthHeaderCommand = "header_command"
)

type Client interface {
	Embed(ctx context.Context, req Request) (Response, error)
}

type Request struct {
	Model string
	Input string
}

type Response struct {
	Vector []float32
	Model  string
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

func NewWithOptions(provider string, options Options) (Client, error) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case ProviderOpenAI:
		endpoint, err := endpointFromBaseValue(options.BaseURL, "OPENAI_BASE_URL", openAIDefaultBaseURL, openAIEmbeddingsPath)
		if err != nil {
			return nil, err
		}
		headers, err := requestHeadersWithOptions("OPENAI_API_KEY", "authorization", func(apiKey string) string {
			return "Bearer " + apiKey
		}, options)
		if err != nil {
			return nil, err
		}
		return newOpenAIClient(endpoint, headers, defaultHTTPClient()), nil
	default:
		return nil, fmt.Errorf("embed: unsupported provider %q", provider)
	}
}

func validateRequest(req Request) error {
	if strings.TrimSpace(req.Model) == "" {
		return errors.New("embed: model is required")
	}
	if strings.TrimSpace(req.Input) == "" {
		return errors.New("embed: input is required")
	}
	return nil
}

func defaultHTTPClient() *http.Client {
	return &http.Client{Timeout: defaultTimeout}
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
		baseSource = "search.base_url"
	}

	endpoint := strings.TrimRight(base, "/")
	if !strings.HasSuffix(endpoint, path) {
		endpoint += path
	}

	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("embed: invalid %s: %w", baseSource, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("embed: invalid %s: expected absolute URL", baseSource)
	}
	return endpoint, nil
}

func requestHeadersWithOptions(apiKeyEnv string, authHeaderName string, authHeaderValue func(string) string, options Options) (map[string]string, error) {
	headers := map[string]string{}
	if err := mergeHeaders(headers, options.Headers, "search.headers"); err != nil {
		return nil, err
	}

	key, value, err := authHeader(apiKeyEnv, authHeaderName, authHeaderValue, options.Auth)
	if err != nil {
		return nil, err
	}
	headers[key] = value
	return headers, nil
}

func authHeader(apiKeyEnv string, authHeaderName string, authHeaderValue func(string) string, auth AuthConfig) (string, string, error) {
	switch strings.TrimSpace(auth.Type) {
	case "", AuthProviderEnv:
		apiKey := strings.TrimSpace(os.Getenv(apiKeyEnv))
		if apiKey == "" {
			return "", "", fmt.Errorf("embed: %s is not set", apiKeyEnv)
		}
		return authHeaderName, authHeaderValue(apiKey), nil
	case AuthHeaderEnv:
		envName := strings.TrimSpace(auth.Env)
		if envName == "" {
			return "", "", errors.New("embed: auth env is required for header_env")
		}
		header := strings.TrimSpace(os.Getenv(envName))
		if header == "" {
			return "", "", fmt.Errorf("embed: %s is not set", envName)
		}
		return parseHeaderNamed(envName, header)
	case AuthHeaderCommand:
		return authHeaderFromCommand(auth.Command)
	default:
		return "", "", fmt.Errorf("embed: unsupported auth type %q", auth.Type)
	}
}

func mergeHeaders(dst map[string]string, src map[string]string, source string) error {
	for key, value := range src {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			return fmt.Errorf("embed: %s contains an empty header", source)
		}
		dst[key] = value
	}
	return nil
}

func parseHeaderNamed(name string, value string) (string, string, error) {
	key, headerValue, ok := strings.Cut(value, ":")
	if !ok {
		return "", "", fmt.Errorf("embed: %s must be formatted as 'Header-Name: value'", name)
	}
	key = strings.TrimSpace(key)
	headerValue = strings.TrimSpace(headerValue)
	if key == "" || headerValue == "" {
		return "", "", fmt.Errorf("embed: %s must contain a non-empty header name and value", name)
	}
	return key, headerValue, nil
}

func authHeaderFromCommand(command []string) (string, string, error) {
	if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
		return "", "", errors.New("embed: auth command is required")
	}
	name := strings.TrimSpace(command[0])
	args := make([]string, 0, len(command)-1)
	for _, arg := range command[1:] {
		args = append(args, strings.TrimSpace(arg))
	}
	output, err := exec.Command(name, args...).Output()
	if err != nil {
		return "", "", fmt.Errorf("embed: auth command failed: %w", err)
	}
	return parseHeaderNamed("search.auth.command", strings.TrimSpace(string(output)))
}
