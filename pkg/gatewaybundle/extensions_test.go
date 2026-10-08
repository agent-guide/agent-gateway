package gatewaybundle

import (
	"context"
	"encoding/json"
	"github.com/agent-guide/agent-gateway/pkg/extension"
	"reflect"
	"strings"
	"testing"
)

type resource struct{}

func (resource) Health(context.Context) error { return nil }
func (resource) Close(context.Context) error  { return nil }
func TestExtensionReferenceRoundTrip(t *testing.T) {
	r, err := extension.NewRegistry(extension.Registration{Descriptor: extension.Descriptor{ID: "example.fixture.secret", Version: "1.0.0", SchemaVersion: 1, Roles: []string{"validation"}}, Factory: &extension.Factory{SecretFields: []string{"/token"}, Validate: func(_ context.Context, b json.RawMessage) (json.RawMessage, error) { return b, nil }, Prepare: func(context.Context, extension.PreparedConfig, extension.Snapshot) (extension.Resource, error) {
		return resource{}, nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("BUNDLE_F2_SECRET", "never-serialize")
	t.Setenv("BUNDLE_F2_HOST", "localhost")
	bundle, err := DecodeYAML([]byte("apiVersion: gateway.agw/v1alpha1\nkind: GatewayBundle\nextensions:\n- module_id: example.fixture.secret\n  schema_version: 1\n  config:\n    token: {\"$secret_ref\": \"env:BUNDLE_F2_SECRET\"}\n    host: '${BUNDLE_F2_HOST}'\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err = bundle.ValidateWithRegistry(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeYAML(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "never-serialize") || !strings.Contains(string(encoded), "env:BUNDLE_F2_SECRET") || !strings.Contains(string(encoded), "localhost") {
		t.Fatalf("bad export: %s", encoded)
	}
	if err = bundle.Validate(); err == nil {
		t.Fatal("validated config without registry")
	}
	bundle.Extensions[0].Config = json.RawMessage(`{"token":"${BUNDLE_F2_SECRET}"}`)
	if err = bundle.ValidateExtensions(context.Background(), r); err == nil {
		t.Fatal("expanded literal secret accepted")
	}
}

func TestExtensionIntegerExportRoundTrip(t *testing.T) {
	raw := json.RawMessage(`{"positive":9007199254740993,"negative":-9223372036854775807,"signed_max":9223372036854775807,"unsigned_max":18446744073709551615,"nested":[9007199254740993],"fraction":1.25}`)
	original := &GatewayBundle{APIVersion: APIVersionV1Alpha1, Kind: KindGatewayBundle, Extensions: []extension.Envelope{{ModuleID: "example.fixture.numeric", SchemaVersion: 1, Config: raw, Source: "static"}}}
	encoded, err := EncodeYAML(original)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"9007199254740993", "-9223372036854775807", "9223372036854775807", "18446744073709551615"} {
		if !strings.Contains(string(encoded), value) {
			t.Fatalf("number lost in export: %s", encoded)
		}
	}
	decoded, err := DecodeYAML(encoded)
	if err != nil {
		t.Fatal(err)
	}
	want, err := extension.DecodeObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := extension.DecodeObject(decoded.Extensions[0].Config)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("numeric config changed: %s", decoded.Extensions[0].Config)
	}
	if decoded.Extensions[0].Source != "static" {
		t.Fatal("ownership lost")
	}
}
