package crusoe_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/crusoecloud/crusoe-csi-driver/internal/crusoe"
)

//nolint:paralleltest // reads and restores the process-wide http.DefaultClient
func TestNewCrusoeClientLeavesDefaultClientUnsigned(t *testing.T) {
	original := http.DefaultClient.Transport
	t.Cleanup(func() { http.DefaultClient.Transport = original })

	headers := make(chan http.Header, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{}`)); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(srv.Close)

	secret := base64.RawURLEncoding.EncodeToString([]byte("test-secret"))
	// The driver builds a client in more than one place, so build two.
	crusoe.NewCrusoeClient(srv.URL, "test-key", secret, "test-agent")
	client := crusoe.NewCrusoeClient(srv.URL, "test-key", secret, "test-agent")

	if http.DefaultClient.Transport != original {
		t.Errorf("http.DefaultClient.Transport was replaced: %#v", http.DefaultClient.Transport)
	}

	unsigned := requestHeaders(t, headers, func() *http.Response {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}

		return resp
	})
	if got := unsigned.Get("Authorization"); got != "" {
		t.Errorf("http.DefaultClient request carried Crusoe credentials: %q", got)
	}
	if got := unsigned.Get("X-Crusoe-Timestamp"); got != "" {
		t.Errorf("http.DefaultClient request carried a Crusoe timestamp: %q", got)
	}

	signed := requestHeaders(t, headers, func() *http.Response {
		_, resp, err := client.ProjectsApi.GetProject(context.Background(), "p")
		if err != nil {
			t.Fatal(err)
		}

		return resp
	})
	if got := signed.Get("Authorization"); !strings.HasPrefix(got, "Bearer 1.0:test-key:") {
		t.Errorf("Crusoe client request was not signed: %q", got)
	}
	if signed.Get("X-Crusoe-Timestamp") == "" {
		t.Error("Crusoe client request has no timestamp")
	}
}

// requestHeaders runs do and returns the headers the test server received.
func requestHeaders(t *testing.T, headers <-chan http.Header, do func() *http.Response) http.Header {
	t.Helper()
	if err := do().Body.Close(); err != nil {
		t.Fatal(err)
	}

	return <-headers
}
