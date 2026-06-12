package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const maxErrorBodyChars = 4096

func postJSON(ctx context.Context, client *http.Client, endpoint string, headers map[string]string, payload any, response any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("content-type", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}

	resp, err := client.Do(request)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("llm: request failed with %s: %s", resp.Status, readErrorBody(resp.Body))
	}

	decoder := json.NewDecoder(resp.Body)
	return decoder.Decode(response)
}

func readErrorBody(reader io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(reader, maxErrorBodyChars))
	if err != nil {
		return "failed to read error body"
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return "<empty response body>"
	}
	return text
}
