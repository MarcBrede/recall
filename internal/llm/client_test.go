package llm

import "testing"

func TestEndpointFromBaseAppendsPath(t *testing.T) {
	t.Setenv("TEST_BASE_URL", "https://example.test")

	got, err := endpointFromBase("TEST_BASE_URL", "https://default.test", "/v1/messages")
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://example.test/v1/messages"; got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
}

func TestEndpointFromBaseAcceptsFullEndpoint(t *testing.T) {
	t.Setenv("TEST_BASE_URL", "https://example.test/v1/messages")

	got, err := endpointFromBase("TEST_BASE_URL", "https://default.test", "/v1/messages")
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://example.test/v1/messages"; got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
}

func TestRequestHeadersUsesAuthHeaderOverrideAndExtraHeaders(t *testing.T) {
	t.Setenv(envAuthHeader, "Authorization: Bearer token")
	t.Setenv(envHeaders, `{"source":"recall-test","org-id":"2"}`)

	headers, err := requestHeaders("TEST_API_KEY", "x-api-key", func(apiKey string) string {
		return apiKey
	}, map[string]string{
		"anthropic-version": anthropicVersion,
	})
	if err != nil {
		t.Fatal(err)
	}

	wants := map[string]string{
		"Authorization":     "Bearer token",
		"source":            "recall-test",
		"org-id":            "2",
		"anthropic-version": anthropicVersion,
	}
	for key, want := range wants {
		if got := headers[key]; got != want {
			t.Fatalf("header %q = %q, want %q", key, got, want)
		}
	}
}

func TestRequestHeadersUsesProviderAPIKey(t *testing.T) {
	t.Setenv("TEST_API_KEY", "provider-key")

	headers, err := requestHeaders("TEST_API_KEY", "authorization", func(apiKey string) string {
		return "Bearer " + apiKey
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := headers["authorization"], "Bearer provider-key"; got != want {
		t.Fatalf("authorization = %q, want %q", got, want)
	}
}
