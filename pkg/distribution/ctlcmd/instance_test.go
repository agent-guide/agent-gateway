package ctlcmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestIndependentCommandsCanExecuteConcurrently(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/credentials" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]string{{"id": "test-credential", "type": "oauth_token"}}})
	}))
	defer srv.Close()
	var wg sync.WaitGroup
	for _, format := range []string{"table", "json"} {
		cmd := NewCommand(Options{})
		var out, stderr bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&stderr)
		cmd.SetArgs([]string{"--admin-addr", srv.URL, "-o", format, "credential", "list"})
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := cmd.Execute(); err != nil {
				t.Errorf("execute: %v: %s", err, &stderr)
				return
			}
			if !strings.Contains(out.String(), "test-credential") {
				t.Errorf("missing credential in %s", &out)
			}
			if json.Valid(out.Bytes()) != (format == "json") {
				t.Errorf("%s output leaked other instance state: %s", format, &out)
			}
		}()
	}
	wg.Wait()
	fresh := NewCommand(Options{})
	if got, _ := fresh.PersistentFlags().GetString("output"); got != "table" {
		t.Fatalf("fresh output=%q", got)
	}
	if fresh.PersistentFlags().Changed("admin-addr") {
		t.Fatal("fresh command retained Changed flag")
	}
}
