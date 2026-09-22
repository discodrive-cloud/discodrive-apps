package protocol

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEmbeddedClientIgnoresInsecureEnvironment(t *testing.T) {
	t.Setenv("DISCODRIVE_INSECURE_TLS", "1")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("untrusted server reached") }))
	defer server.Close()
	if _, err := NewStrict(server.URL, "token").SyncMeta(context.Background()); err == nil {
		t.Fatal("accepted untrusted certificate")
	}
}
