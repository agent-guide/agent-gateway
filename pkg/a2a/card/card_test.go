package card

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchClassifiesCardAndFiltersInterfaces(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json" || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected request headers: %#v", r.Header)
		}
		w.Header().Set("Content-Type", "Application/JSON; Charset=UTF-8")
		w.Header().Set("ETag", "\"v1\"")
		fmt.Fprintf(w, "{\"name\":\"remote\",\"description\":\"\",\"version\":\"1\",\"capabilities\":{\"streaming\":true},"+
			"\"defaultInputModes\":[\"text/plain\"],\"defaultOutputModes\":[\"text/plain\"],\"skills\":[],"+
			"\"supportedInterfaces\":[{\"url\":\"https://ignored.example/a2a\",\"protocolBinding\":\"REST\",\"protocolVersion\":\"1.0\"},"+
			"{\"url\":%q,\"protocolBinding\":\"JSONRPC\",\"protocolVersion\":\"1.0\",\"tenant\":\"tenant-a\"},"+
			"{\"url\":%q,\"protocolBinding\":\"JSON-RPC\",\"protocolVersion\":\"1.0\"}],"+
			"\"securitySchemes\":{\"bearer\":{\"httpAuthSecurityScheme\":{\"scheme\":\"Bearer\"}}},"+
			"\"securityRequirements\":[{\"schemes\":{\"missing\":[]}},{\"schemes\":{\"bearer\":[]}}]}",
			server.URL+"/a2a", server.URL+"/wrong")
	}))
	defer server.Close()

	got, notModified, err := Fetch(context.Background(), server.Client(), server.URL+"/card", Validators{})
	if err != nil || notModified {
		t.Fatalf("Fetch() = %#v, %v, %v", got, notModified, err)
	}
	if len(got.Interfaces) != 1 || got.Interfaces[0].URL != server.URL+"/a2a" || got.Interfaces[0].Tenant != "tenant-a" {
		t.Fatalf("interfaces = %#v", got.Interfaces)
	}
	if len(got.Security) != 2 || got.Security[0].Kind != SecurityUnsupported || got.Security[1].Kind != SecurityBearer {
		t.Fatalf("security = %#v", got.Security)
	}
	if got.ETag != "\"v1\"" {
		t.Fatalf("etag = %q", got.ETag)
	}
}

func TestFetchRejectsRedirectAndTrailingJSON(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "{}{}")
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.RedirectHandler(target.URL, http.StatusFound))
	defer redirect.Close()
	if _, _, err := Fetch(context.Background(), redirect.Client(), redirect.URL, Validators{}); err == nil {
		t.Fatal("redirect accepted")
	}
	if _, _, err := Fetch(context.Background(), target.Client(), target.URL, Validators{}); err == nil {
		t.Fatal("trailing JSON accepted")
	}
}
