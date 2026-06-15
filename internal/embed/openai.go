package embed

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

const (
	openAIDefaultBaseURL = "https://api.openai.com"
	openAIEmbeddingsPath = "/v1/embeddings"
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

func (client openAIClient) Embed(ctx context.Context, req Request) (Response, error) {
	if err := validateRequest(req); err != nil {
		return Response{}, err
	}

	payload := map[string]any{
		"model": req.Model,
		"input": req.Input,
	}

	var response openAIEmbeddingResponse
	if err := postJSON(ctx, client.httpClient, client.endpoint, client.headers, payload, &response); err != nil {
		return Response{}, err
	}
	if len(response.Data) == 0 {
		return Response{}, errors.New("embed: OpenAI response did not contain embeddings")
	}
	if len(response.Data[0].Embedding) == 0 {
		return Response{}, errors.New("embed: OpenAI embedding is empty")
	}
	model := response.Model
	if model == "" {
		model = req.Model
	}
	return Response{
		Vector: response.Data[0].Embedding,
		Model:  model,
	}, nil
}

type openAIEmbeddingResponse struct {
	Model string                    `json:"model"`
	Data  []openAIEmbeddingDataItem `json:"data"`
}

type openAIEmbeddingDataItem struct {
	Embedding []float32 `json:"embedding"`
}

func (response openAIEmbeddingResponse) String() string {
	return fmt.Sprintf("model=%s embeddings=%d", response.Model, len(response.Data))
}
