package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
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

func (client anthropicClient) GenerateStructured(ctx context.Context, req StructuredRequest) (string, error) {
	if err := validateStructuredRequest(req); err != nil {
		return "", err
	}

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
		return "", err
	}

	return response.structuredText()
}

type anthropicResponse struct {
	Content    []anthropicContent `json:"content"`
	StopReason string             `json:"stop_reason"`
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
