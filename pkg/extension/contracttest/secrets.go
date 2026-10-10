package contracttest

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/extension"
)

// SecretReferences checks syntax-only validation with an absent/present env
// variable and reference-preserving manager/store round trips. Input must use a
// declared env reference to variable; factories own resolution assertions.
func SecretReferences(t *testing.T, registrations []extension.Registration, input []extension.Envelope, variable, secret string) {
	t.Helper()
	r, err := extension.NewRegistry(registrations...)
	if err != nil {
		t.Fatal(err)
	}
	// Setenv registers restoration and rejects parallel use before unsetting.
	t.Setenv(variable, "")
	if err := os.Unsetenv(variable); err != nil {
		t.Fatal(err)
	}
	absent, err := r.Validate(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(variable, secret)
	present, err := r.Validate(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(absent, present) {
		t.Fatal("environment changed syntax-only validation")
	}
	before, err := json.Marshal(present)
	if err != nil {
		t.Fatal(err)
	}
	if len(input) == 0 || secret == "" || !bytes.Contains(before, []byte("env:"+variable)) || bytes.Contains(before, []byte(secret)) {
		t.Fatal("invalid secret-reference fixture")
	}
	store := &persistence{items: present}
	m, err := extension.NewManager(t.Context(), r, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(context.Background())
	for _, e := range present {
		if err = m.Put(t.Context(), e); err != nil {
			t.Fatal(err)
		}
	}
	listed := m.List()
	for i := range listed {
		listed[i].Source = ""
	}
	after, err := json.Marshal(listed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("manager changed secret reference")
	}
	persisted, err := json.Marshal(store.items)
	if err != nil || !bytes.Equal(before, persisted) {
		t.Fatal("store changed secret reference")
	}
}
