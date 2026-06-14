package llm

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

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
	t.Setenv(envAuthHeader, "")
	t.Setenv(envHeaders, "")
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

func TestNewWithOptionsUsesBaseURLHeadersAndHeaderEnvAuth(t *testing.T) {
	t.Setenv(envAuthHeader, "")
	t.Setenv(envHeaders, "")
	t.Setenv("TEST_AUTH_HEADER", "Authorization: Bearer env-token")

	client, err := NewWithOptions(providerAnthropic, Options{
		BaseURL: "https://gateway.test",
		Headers: map[string]string{
			"source": "recall-test",
		},
		Auth: AuthConfig{
			Type: AuthHeaderEnv,
			Env:  "TEST_AUTH_HEADER",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	anthropic, ok := client.(anthropicClient)
	if !ok {
		t.Fatalf("client type = %T, want anthropicClient", client)
	}
	if got, want := anthropic.endpoint, "https://gateway.test/v1/messages"; got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
	wants := map[string]string{
		"Authorization":     "Bearer env-token",
		"source":            "recall-test",
		"anthropic-version": anthropicVersion,
	}
	for key, want := range wants {
		if got := anthropic.headers[key]; got != want {
			t.Fatalf("header %q = %q, want %q", key, got, want)
		}
	}
}

func TestNewWithOptionsUsesHeaderCommandAuth(t *testing.T) {
	t.Setenv(envAuthHeader, "")
	t.Setenv(envHeaders, "")

	client, err := NewWithOptions(providerOpenAI, Options{
		BaseURL: "https://gateway.test",
		Auth: AuthConfig{
			Type:    AuthHeaderCommand,
			Command: []string{"sh", "-c", "printf '%s\\n' 'Authorization: Bearer command-token'"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	openAI, ok := client.(openAIClient)
	if !ok {
		t.Fatalf("client type = %T, want openAIClient", client)
	}
	if got, want := openAI.endpoint, "https://gateway.test/v1/responses"; got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
	if got, want := openAI.headers["Authorization"], "Bearer command-token"; got != want {
		t.Fatalf("authorization = %q, want %q", got, want)
	}
}

func TestLimiterCapsConcurrency(t *testing.T) {
	limiter := NewLimiter(2)

	var current int32
	var maxSeen int32
	var wg sync.WaitGroup
	errs := make(chan error, 8)

	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := limiter.Do(context.Background(), func(context.Context) (Response, error) {
				now := atomic.AddInt32(&current, 1)
				for {
					max := atomic.LoadInt32(&maxSeen)
					if now <= max || atomic.CompareAndSwapInt32(&maxSeen, max, now) {
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
				atomic.AddInt32(&current, -1)
				return Response{}, nil
			})
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&maxSeen); got > 2 {
		t.Fatalf("max concurrency = %d, want <= 2", got)
	}
}
