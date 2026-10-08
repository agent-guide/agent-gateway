package extensionconsumer

import (
	"bytes"
	"encoding/json"
	"github.com/agent-guide/agent-gateway/pkg/distribution"
	"github.com/agent-guide/agent-gateway/pkg/distribution/caddycmd"
	"github.com/agent-guide/agent-gateway/pkg/distribution/ctlcmd"
	"github.com/agent-guide/agent-gateway/pkg/distribution/daemoncmd"
	"github.com/agent-guide/agent-gateway/pkg/extension"
	"github.com/agent-guide/agent-gateway/pkg/extension/contracttest"
	"reflect"
	"testing"
)

func TestProtectedExtensionContracts(t *testing.T) {
	a := Runtime()
	var regs []extension.Registration
	for _, d := range a.Modules {
		regs = append(regs, extension.Registration{Descriptor: d, Factory: a.Factories[d.ID]})
	}
	t.Setenv("AGW_FIXTURE_SECRET", "fixture-secret-never-export")
	contracttest.Registry(t, regs)
	contracttest.Configuration(t, regs, []extension.Envelope{{ModuleID: "example.fixture.entries", SchemaVersion: 1, Config: json.RawMessage(`{"message":"hello","token":{"$secret_ref":"env:AGW_FIXTURE_SECRET"}}`)}})
}
func TestManifestProjectionAndOwnedFactories(t *testing.T) {
	a := Runtime()
	manifest, err := caddycmd.New(a).Manifest()
	if err != nil {
		t.Fatal(err)
	}
	command := daemoncmd.NewCommand(daemoncmd.Options{Assembly: a})
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetArgs([]string{"manifest"})
	if err = command.Execute(); err != nil {
		t.Fatal(err)
	}
	var got distribution.Assembly
	if err = json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(manifest, got) {
		t.Fatal("server projections differ")
	}
	copy := a.Clone()
	copy.Factories["example.fixture.entries"].SecretFields[0] = "/changed"
	if a.Factories["example.fixture.entries"].SecretFields[0] != "/token" {
		t.Fatal("factory alias")
	}
	cli := ctlcmd.NewCommand(ctlcmd.Options{Assembly: Validation()})
	out.Reset()
	cli.SetOut(&out)
	cli.SetArgs([]string{"manifest"})
	if err = cli.Execute(); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, d := range got.Modules {
		if !reflect.DeepEqual(d.Roles, []string{"validation"}) {
			t.Fatal("CLI claims server execution")
		}
	}
}
