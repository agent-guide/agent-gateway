package authenticator

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestCallbackHTTPServerAssignsDynamicLocalPort(t *testing.T) {
	srv := newCallbackHTTPServer(0)
	if err := srv.start(http.NewServeMux()); err != nil {
		t.Fatalf("start() error = %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.stop(ctx)
	}()

	if got := srv.actualPort(); got <= 0 {
		t.Fatalf("actualPort() = %d, want assigned port", got)
	}
}
