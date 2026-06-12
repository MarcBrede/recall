package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIGenerateStructured(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got, want := request.Header.Get("authorization"), "Bearer test-key"; got != want {
			t.Fatalf("authorization header = %q, want %q", got, want)
		}

		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if got, want := payload["model"], "gpt-test"; got != want {
			t.Fatalf("model = %q, want %q", got, want)
		}
		reasoning := payload["reasoning"].(map[string]any)
		if got, want := reasoning["effort"], "high"; got != want {
			t.Fatalf("reasoning effort = %q, want %q", got, want)
		}

		text := payload["text"].(map[string]any)
		format := text["format"].(map[string]any)
		if got, want := format["type"], "json_schema"; got != want {
			t.Fatalf("format type = %q, want %q", got, want)
		}
		if got, want := format["name"], "test_schema"; got != want {
			t.Fatalf("schema name = %q, want %q", got, want)
		}
		if got, want := format["strict"], true; got != want {
			t.Fatalf("strict = %v, want %v", got, want)
		}

		writer.Header().Set("content-type", "application/json")
		_, _ = writer.Write([]byte(`{
			"status": "completed",
			"output": [
				{
					"type": "message",
					"content": [
						{
							"type": "output_text",
							"text": "{\"session_summary\":\"summary\"}"
						}
					]
				}
			]
		}`))
	}))
	defer server.Close()

	client := newOpenAIClient(server.URL, map[string]string{
		"authorization": "Bearer test-key",
	}, server.Client())
	got, err := client.GenerateStructured(context.Background(), StructuredRequest{
		Model:          "gpt-test",
		ReasoningLevel: "high",
		UserPrompt:     "input",
		SchemaName:     "test_schema",
		Schema:         map[string]any{"type": "object"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"session_summary":"summary"}`; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}
