package embed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIEmbedPostsRequestAndParsesVector(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/v1/embeddings"; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("Authorization"), "Bearer test-token"; got != want {
			t.Fatalf("authorization = %q, want %q", got, want)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if got, want := payload["model"], "embedding-test"; got != want {
			t.Fatalf("model = %q, want %q", got, want)
		}
		if got, want := payload["input"], "hello search"; got != want {
			t.Fatalf("input = %q, want %q", got, want)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"model":"embedding-test","data":[{"embedding":[0.1,0.2,0.3]}]}`))
	}))
	defer server.Close()

	client := newOpenAIClient(server.URL+"/v1/embeddings", map[string]string{
		"Authorization": "Bearer test-token",
	}, server.Client())

	response, err := client.Embed(context.Background(), Request{
		Model: "embedding-test",
		Input: "hello search",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := response.Model, "embedding-test"; got != want {
		t.Fatalf("model = %q, want %q", got, want)
	}
	if got, want := len(response.Vector), 3; got != want {
		t.Fatalf("vector len = %d, want %d", got, want)
	}
}

func TestNewWithOptionsUsesHeaderEnvAuth(t *testing.T) {
	t.Setenv("TEST_SEARCH_AUTH_HEADER", "Authorization: Bearer env-token")

	client, err := NewWithOptions(ProviderOpenAI, Options{
		BaseURL: "https://example.test",
		Headers: map[string]string{
			"x-test-header": "test-value",
		},
		Auth: AuthConfig{
			Type: AuthHeaderEnv,
			Env:  "TEST_SEARCH_AUTH_HEADER",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	openAI, ok := client.(openAIClient)
	if !ok {
		t.Fatalf("client type = %T, want openAIClient", client)
	}
	if got, want := openAI.endpoint, "https://example.test/v1/embeddings"; got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
	if got, want := openAI.headers["Authorization"], "Bearer env-token"; got != want {
		t.Fatalf("authorization = %q, want %q", got, want)
	}
	if got, want := openAI.headers["x-test-header"], "test-value"; got != want {
		t.Fatalf("x-test-header = %q, want %q", got, want)
	}
}
