package contracttest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// AdminBody checks the shared HTTP body/CORS contract through a real mount.
// Send adds deployment credentials and performs the request. URL must identify
// a write accepting Body with SuccessStatus. CheckUnchanged verifies the domain
// state after each rejected request; no mutation may occur on trailing oversize.
type AdminBodyFixture struct {
	URL            string
	Method         string
	Body           []byte
	SuccessStatus  int
	Send           func(*http.Request) (*http.Response, error)
	CheckUnchanged func(*testing.T)
}

func AdminBody(t *testing.T, f AdminBodyFixture) {
	t.Helper()
	const limit = 4194304
	if len(f.Body) > limit || f.Send == nil || f.CheckUnchanged == nil {
		t.Fatal("invalid Admin body fixture")
	}
	for _, tc := range []struct {
		name     string
		unknown  bool
		encoding string
		oversize bool
		status   int
	}{
		{name: "known_length_oversize", oversize: true, status: 413},
		{name: "chunked_oversize", unknown: true, oversize: true, status: 413},
		{name: "compressed", encoding: "gzip", status: 415},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := bytes.Clone(f.Body)
			if tc.oversize {
				body = append(body, bytes.Repeat([]byte(" "), limit+1-len(body))...)
			}
			req, err := http.NewRequest(f.Method, f.URL, bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			if tc.unknown {
				req.ContentLength = -1
			}
			if tc.encoding != "" {
				req.Header.Set("Content-Encoding", tc.encoding)
			}
			req.Header.Set("Origin", "https://admin.example")
			resp, err := f.Send(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			raw, err := io.ReadAll(io.LimitReader(resp.Body, 1025))
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tc.status || len(raw) > 1024 || !json.Valid(raw) {
				t.Fatalf("boundary: %d %s", resp.StatusCode, raw)
			}
			if tc.oversize && strings.TrimSpace(string(raw)) != `{"error":"request body exceeds 4194304 bytes"}` {
				t.Fatalf("oversize diagnostic: %s", raw)
			}
			if resp.Header.Get("Access-Control-Allow-Origin") != "https://admin.example" {
				t.Fatal("missing shared CORS")
			}
			f.CheckUnchanged(t)
		})
	}
	req, err := http.NewRequest(f.Method, f.URL, bytes.NewReader(f.Body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := f.Send(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != f.SuccessStatus {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		t.Fatalf("supported write: %d %s", resp.StatusCode, raw)
	}
}
