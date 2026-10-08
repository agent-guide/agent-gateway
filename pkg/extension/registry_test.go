package extension

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

func descriptor(id string) Descriptor {
	return Descriptor{ID: id, Version: "1.0.0", SchemaVersion: 1, Roles: []string{"runtime", "validation"}}
}
func factory() *Factory {
	return &Factory{Validate: func(_ context.Context, b json.RawMessage) (json.RawMessage, error) { return b, nil }, Prepare: func(context.Context, PreparedConfig, Snapshot) (Resource, error) { return &testResource{}, nil }}
}

type testResource struct{}

func (*testResource) Health(context.Context) error { return nil }
func (*testResource) Close(context.Context) error  { return nil }
func code(t *testing.T, err error, want string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != want {
		t.Fatalf("got %v, want %s", err, want)
	}
}
func TestRegistryContracts(t *testing.T) {
	a, b := descriptor("example.test.a"), descriptor("example.test.b")
	b.Dependencies = []string{a.ID}
	r, err := NewRegistry(Registration{b, nil}, Registration{a, nil})
	if err != nil {
		t.Fatal(err)
	}
	if r.Manifest().Modules[0].ID != a.ID {
		t.Fatal("unordered")
	}
	b.Dependencies[0] = "changed"
	m := r.Manifest()
	m.Modules[0].Roles[0] = "changed"
	if r.Manifest().Modules[0].Roles[0] == "changed" {
		t.Fatal("aliased")
	}
	_, err = NewRegistry(Registration{a, nil}, Registration{a, nil})
	code(t, err, "duplicate_module")
	_, err = NewRegistry(Registration{})
	code(t, err, "invalid_registration")
	b = descriptor("example.test.b")
	b.Dependencies = []string{a.ID}
	_, err = NewRegistry(Registration{b, nil})
	code(t, err, "missing_dependency")
	a.Dependencies = []string{b.ID}
	_, err = NewRegistry(Registration{a, nil}, Registration{b, nil})
	code(t, err, "dependency_cycle")
	empty, err := NewRegistry()
	if err != nil || len(empty.Manifest().Modules) != 0 {
		t.Fatal(err)
	}
}
func TestConfigSecretsAndBounds(t *testing.T) {
	d := descriptor("example.test.secret")
	f := factory()
	f.SecretFields = []string{"/token"}
	r, err := NewRegistry(Registration{d, f})
	if err != nil {
		t.Fatal(err)
	}
	e := Envelope{ModuleID: d.ID, SchemaVersion: 1, Config: json.RawMessage(`{"token":{"$secret_ref":"env:EXT_TEST_SECRET"}}`)}
	t.Setenv("EXT_TEST_SECRET", "do-not-export")
	out, err := r.Validate(context.Background(), []Envelope{e})
	if err != nil {
		t.Fatal(err)
	}
	if string(out[0].Config) != string(e.Config) {
		t.Fatal("token lost")
	}
	cfg, err := resolve(e, f)
	if err != nil || cfg.Secrets["/token"] != "do-not-export" {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"token":"literal"}`, `{"other":{"$secret_ref":"env:EXT_TEST_SECRET"}}`, `{"token":{"$secret_ref":"${EXT_TEST_SECRET}"}}`} {
		e.Config = json.RawMessage(raw)
		_, err = r.Validate(context.Background(), []Envelope{e})
		code(t, err, "invalid_secret_ref")
	}
	e.Config = json.RawMessage(`{}`)
	e.SchemaVersion = 2
	_, err = r.Validate(context.Background(), []Envelope{e})
	code(t, err, "schema_mismatch")
	e.SchemaVersion = 1
	e.ModuleID = "example.test.unknown"
	_, err = r.Validate(context.Background(), []Envelope{e})
	code(t, err, "unknown_module")
	raw := "{}"
	for range 34 {
		raw = `{"x":` + raw + `}`
	}
	_, err = DecodeObject(json.RawMessage(raw))
	code(t, err, "invalid_config")
	f.Validate = func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("secret-value")
	}
	r, _ = NewRegistry(Registration{d, f})
	e.ModuleID = d.ID
	_, err = r.Validate(context.Background(), []Envelope{e})
	code(t, err, "invalid_config")
	if err.Error() == "secret-value" {
		t.Fatal("leak")
	}
}

func TestRegistrationAndConfigurationLimits(t *testing.T) {
	d := descriptor("example.test.bounds")
	for _, mutate := range []func(*Descriptor){func(d *Descriptor) { d.SchemaVersion = 0 }, func(d *Descriptor) { d.Version = "1.0.0-01" }, func(d *Descriptor) { d.Roles = []string{"unknown"} }, func(d *Descriptor) { d.Capabilities = []string{"example.test.same", "example.test.same"} }, func(d *Descriptor) { d.Dependencies = make([]string, 65) }} {
		bad := d
		mutate(&bad)
		_, err := NewRegistry(Registration{bad, nil})
		code(t, err, "invalid_registration")
	}
	build := d
	build.Version = "1.0.0+build-01"
	if _, err := NewRegistry(Registration{build, nil}); err != nil {
		t.Fatal(err)
	}
	_, err := NewRegistry(make([]Registration, 257)...)
	code(t, err, "invalid_registration")
	r, _ := NewRegistry(Registration{d, factory()})
	e := Envelope{ModuleID: d.ID, SchemaVersion: 1, Config: json.RawMessage(`{}`)}
	_, err = r.Validate(context.Background(), make([]Envelope, 65))
	code(t, err, "invalid_config")
	_, err = r.Validate(context.Background(), []Envelope{e, e})
	code(t, err, "duplicate_module")
	large := `{"x":"` + strings.Repeat("x", 256*1024) + `"}`
	e.Config = json.RawMessage(large)
	_, err = r.Validate(context.Background(), []Envelope{e})
	code(t, err, "invalid_config")
	e.Config = json.RawMessage(`{"token":{"$secret_ref":"env:EXT_F2_UNSET_REQUIRED"}}`)
	f := factory()
	f.SecretFields = []string{"/token"}
	r, _ = NewRegistry(Registration{d, f})
	t.Setenv("EXT_F2_UNSET_REQUIRED", "temporary")
	_ = os.Unsetenv("EXT_F2_UNSET_REQUIRED")
	if _, err = r.Validate(context.Background(), []Envelope{e}); err != nil {
		t.Fatal("local validation resolved reference:", err)
	}
	_, err = resolve(e, f)
	code(t, err, "secret_unavailable")
}
func TestConfigSelectedDependencies(t *testing.T) {
	a, b := descriptor("example.test.selected-a"), descriptor("example.test.selected-b")
	f := factory()
	f.Dependencies = func(ctx context.Context, _ json.RawMessage) ([]string, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("dependency validation budget missing")
		}
		return []string{b.ID}, nil
	}
	r, err := NewRegistry(Registration{a, f}, Registration{b, factory()})
	if err != nil {
		t.Fatal(err)
	}
	e := Envelope{ModuleID: a.ID, SchemaVersion: 1, Config: json.RawMessage(`{}`)}
	_, err = NewManager(context.Background(), r, nil, []Envelope{e})
	code(t, err, "missing_dependency")
	e2 := Envelope{ModuleID: b.ID, SchemaVersion: 1, Config: json.RawMessage(`{}`)}
	m, err := NewManager(context.Background(), r, nil, []Envelope{e, e2})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(context.Background())
	if m.List()[0].ModuleID != b.ID {
		t.Fatal("selected dependency order ignored")
	}
}

func TestDuplicateJSONKeysCannotHideSecrets(t *testing.T) {
	d := descriptor("example.test.duplicates")
	f := factory()
	f.SecretFields = []string{"/token"}
	r, err := NewRegistry(Registration{d, f})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"token":"literal-secret","token":{"$secret_ref":"env:REVIEW_TOKEN"}}`,
		`{"other":{"$secret_ref":"env:REVIEW_TOKEN"},"other":"plain"}`,
		`{"token":{"$secret_ref":"env:FIRST","$secret_ref":"env:SECOND"}}`,
		`{"nested":[{"x":1,"x":2}]}`,
	} {
		e := Envelope{ModuleID: d.ID, SchemaVersion: 1, Config: json.RawMessage(raw)}
		_, err = r.Validate(context.Background(), []Envelope{e})
		code(t, err, "invalid_config")
		_, err = r.ExpandConfig([]Envelope{e})
		code(t, err, "invalid_config")
		if strings.Contains(err.Error(), "literal-secret") {
			t.Fatal("literal echoed in diagnostic")
		}
	}
}

func TestTopologicalTiesUseLexicalReadyOrder(t *testing.T) {
	a, b, z := descriptor("example.test.a"), descriptor("example.test.b"), descriptor("example.test.z")
	a.Dependencies = []string{z.ID}
	registrations := []Registration{{a, factory()}, {b, factory()}, {z, factory()}}
	r, err := NewRegistry(registrations...)
	if err != nil {
		t.Fatal(err)
	}
	input := []Envelope{{ModuleID: a.ID, SchemaVersion: 1, Config: json.RawMessage(`{}`)}, {ModuleID: b.ID, SchemaVersion: 1, Config: json.RawMessage(`{}`)}, {ModuleID: z.ID, SchemaVersion: 1, Config: json.RawMessage(`{}`)}}
	want := []string{b.ID, z.ID, a.ID}
	for range 2 {
		out, err := r.Validate(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		ids := []string{}
		for _, e := range out {
			ids = append(ids, e.ModuleID)
		}
		if !slices.Equal(ids, want) {
			t.Fatalf("order=%v want=%v", ids, want)
		}
		slices.Reverse(input)
	}
	// A newly unblocked module must also outrank an already-ready later ID.
	b.Dependencies = nil
	a.Dependencies = []string{b.ID}
	r, err = NewRegistry(Registration{a, factory()}, Registration{b, factory()}, Registration{z, factory()})
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Validate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, e := range out {
		ids = append(ids, e.ModuleID)
	}
	if !slices.Equal(ids, []string{b.ID, a.ID, z.ID}) {
		t.Fatalf("newly ready order=%v", ids)
	}
}
