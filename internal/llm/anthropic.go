package llm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/marc-brede/recall/internal/obs"
)

const (
	anthropicDefaultBaseURL = "https://api.anthropic.com"
	anthropicMessagesPath   = "/v1/messages"
	anthropicVersion        = "2023-06-01"
)

type anthropicClient struct {
	endpoint   string
	headers    map[string]string
	httpClient *http.Client
}

func newAnthropicClient(endpoint string, headers map[string]string, httpClient *http.Client) Client {
	return anthropicClient{
		endpoint:   endpoint,
		headers:    headers,
		httpClient: httpClient,
	}
}

func (client anthropicClient) GenerateStructured(ctx context.Context, req StructuredRequest) (Response, error) {
	if err := validateStructuredRequest(req); err != nil {
		return Response{}, err
	}

	log := obs.From(ctx).With(
		slog.String("provider", providerAnthropic),
		slog.String("model", req.Model),
	)
	ctx = obs.Into(ctx, log)

	outputConfig := map[string]any{
		"format": map[string]any{
			"type":   "json_schema",
			"schema": req.Schema,
		},
	}
	if reasoningEnabled(req.ReasoningLevel) {
		outputConfig["effort"] = normalizedReasoningLevel(req.ReasoningLevel)
	}

	payload := map[string]any{
		"model":      req.Model,
		"max_tokens": defaultMaxOutputTokens,
		"messages": []map[string]string{
			{
				"role":    "user",
				"content": req.UserPrompt,
			},
		},
		"output_config": outputConfig,
	}
	if req.SystemPrompt != "" {
		payload["system"] = req.SystemPrompt
	}
	if reasoningEnabled(req.ReasoningLevel) {
		payload["thinking"] = map[string]any{
			"type": "adaptive",
		}
	}

	var response anthropicResponse
	err := postJSON(ctx, client.httpClient, client.endpoint, client.headers, payload, &response)
	if err != nil {
		return Response{}, err
	}

	log.Debug("llm response",
		slog.String("stop_reason", response.StopReason),
		slog.Int("input_tokens", response.Usage.InputTokens),
		slog.Int("output_tokens", response.Usage.OutputTokens))

	text, err := response.structuredText()
	if err != nil {
		return Response{}, err
	}
	return Response{
		Text: text,
		Usage: Usage{
			InputTokens:  response.Usage.InputTokens,
			OutputTokens: response.Usage.OutputTokens,
		},
	}, nil
}

type anthropicResponse struct {
	Content    []anthropicContent `json:"content"`
	StopReason string             `json:"stop_reason"`
	Usage      anthropicUsage     `json:"usage"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (response anthropicResponse) structuredText() (string, error) {
	switch response.StopReason {
	case "max_tokens":
		return "", errors.New("llm: Anthropic response stopped at max_tokens")
	case "refusal":
		return "", errors.New("llm: Anthropic refused the request")
	}

	var parts []string
	for _, content := range response.Content {
		if content.Type == "text" && content.Text != "" {
			parts = append(parts, content.Text)
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("llm: Anthropic response did not contain text content")
	}
	return strings.Join(parts, ""), nil
}
