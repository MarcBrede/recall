package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAnthropicGenerateStructured(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got, want := request.Header.Get("x-api-key"), "test-key"; got != want {
			t.Fatalf("x-api-key header = %q, want %q", got, want)
		}
		if got, want := request.Header.Get("anthropic-version"), anthropicVersion; got != want {
			t.Fatalf("anthropic-version header = %q, want %q", got, want)
		}

		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if got, want := payload["model"], "claude-test"; got != want {
			t.Fatalf("model = %q, want %q", got, want)
		}
		thinking := payload["thinking"].(map[string]any)
		if got, want := thinking["type"], "adaptive"; got != want {
			t.Fatalf("thinking type = %q, want %q", got, want)
		}

		outputConfig := payload["output_config"].(map[string]any)
		if got, want := outputConfig["effort"], "medium"; got != want {
			t.Fatalf("output_config effort = %q, want %q", got, want)
		}
		format := outputConfig["format"].(map[string]any)
		if got, want := format["type"], "json_schema"; got != want {
			t.Fatalf("format type = %q, want %q", got, want)
		}

		writer.Header().Set("content-type", "application/json")
		_, _ = writer.Write([]byte(`{
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 120, "output_tokens": 45},
			"content": [
				{
					"type": "text",
					"text": "{\"session_summary\":\"summary\"}"
				}
			]
		}`))
	}))
	defer server.Close()

	client := newAnthropicClient(server.URL, map[string]string{
		"x-api-key":         "test-key",
		"anthropic-version": anthropicVersion,
	}, server.Client())
	got, err := client.GenerateStructured(context.Background(), StructuredRequest{
		Model:          "claude-test",
		ReasoningLevel: "medium",
		UserPrompt:     "input",
		SchemaName:     "test_schema",
		Schema:         map[string]any{"type": "object"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"session_summary":"summary"}`; got.Text != want {
		t.Fatalf("output = %q, want %q", got.Text, want)
	}
	if got.Usage != (Usage{InputTokens: 120, OutputTokens: 45}) {
		t.Fatalf("usage = %+v, want {120 45}", got.Usage)
	}
}
