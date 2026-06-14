package llm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/marc-brede/recall/internal/obs"
)

const (
	openAIDefaultBaseURL = "https://api.openai.com"
	openAIResponsesPath  = "/v1/responses"
)

type openAIClient struct {
	endpoint   string
	headers    map[string]string
	httpClient *http.Client
}

func newOpenAIClient(endpoint string, headers map[string]string, httpClient *http.Client) Client {
	return openAIClient{
		endpoint:   endpoint,
		headers:    headers,
		httpClient: httpClient,
	}
}

func (client openAIClient) GenerateStructured(ctx context.Context, req StructuredRequest) (Response, error) {
	if err := validateStructuredRequest(req); err != nil {
		return Response{}, err
	}

	log := obs.From(ctx).With(
		slog.String("provider", providerOpenAI),
		slog.String("model", req.Model),
	)
	ctx = obs.Into(ctx, log)

	input := []map[string]string{}
	if req.SystemPrompt != "" {
		input = append(input, map[string]string{
			"role":    "system",
			"content": req.SystemPrompt,
		})
	}
	input = append(input, map[string]string{
		"role":    "user",
		"content": req.UserPrompt,
	})

	payload := map[string]any{
		"model":             req.Model,
		"input":             input,
		"max_output_tokens": defaultMaxOutputTokens,
		"text": map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"name":   req.SchemaName,
				"schema": req.Schema,
				"strict": true,
			},
		},
	}
	if reasoningEnabled(req.ReasoningLevel) {
		payload["reasoning"] = map[string]any{
			"effort": normalizedReasoningLevel(req.ReasoningLevel),
		}
	}

	var response openAIResponse
	err := postJSON(ctx, client.httpClient, client.endpoint, client.headers, payload, &response)
	if err != nil {
		return Response{}, err
	}

	log.Debug("llm response",
		slog.String("status", response.Status),
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

type openAIResponse struct {
	OutputText        string                     `json:"output_text"`
	Status            string                     `json:"status"`
	IncompleteDetails *openAIIncompleteDetails   `json:"incomplete_details"`
	Output            []openAIResponseOutputItem `json:"output"`
	Usage             openAIUsage                `json:"usage"`
}

type openAIUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type openAIIncompleteDetails struct {
	Reason string `json:"reason"`
}

type openAIResponseOutputItem struct {
	Type    string                  `json:"type"`
	Content []openAIResponseContent `json:"content"`
}

type openAIResponseContent struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Refusal string `json:"refusal"`
}

func (response openAIResponse) structuredText() (string, error) {
	if response.Status == "incomplete" {
		if response.IncompleteDetails != nil && response.IncompleteDetails.Reason != "" {
			return "", fmt.Errorf("llm: OpenAI response incomplete: %s", response.IncompleteDetails.Reason)
		}
		return "", errors.New("llm: OpenAI response incomplete")
	}
	if response.OutputText != "" {
		return response.OutputText, nil
	}

	for _, item := range response.Output {
		for _, content := range item.Content {
			if content.Refusal != "" {
				return "", fmt.Errorf("llm: OpenAI refused the request: %s", content.Refusal)
			}
			if content.Type == "output_text" && content.Text != "" {
				return content.Text, nil
			}
		}
	}

	return "", errors.New("llm: OpenAI response did not contain output text")
}
