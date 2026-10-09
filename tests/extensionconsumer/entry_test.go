package extensionconsumer

import (
	"encoding/json"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/attribution"
	"github.com/agent-guide/agent-gateway/pkg/distribution"
	"github.com/agent-guide/agent-gateway/pkg/distribution/caddycmd"
	"github.com/agent-guide/agent-gateway/pkg/distribution/ctlcmd"
	"github.com/agent-guide/agent-gateway/pkg/distribution/daemoncmd"
	"github.com/agent-guide/agent-gateway/pkg/event"
	"github.com/agent-guide/agent-gateway/pkg/extension"
)

func TestExternalAssemblyAndDefensiveCopies(t *testing.T) {
	a := Runtime()
	caddy := caddycmd.New(a)
	daemon := daemoncmd.NewCommand(daemoncmd.Options{Assembly: a})
	var got distribution.Assembly
	if err := json.Unmarshal([]byte(daemon.Annotations["agent-gateway.assembly"]), &got); err != nil {
		t.Fatal(err)
	}
	aSnapshot := caddy.Assembly()
	aSnapshot.Factories = nil
	aSnapshot.EventSinks = nil
	if !reflect.DeepEqual(aSnapshot, got) {
		t.Fatal("server entries differ")
	}
	a.Modules[0].Capabilities[0] = "changed"
	snapshot := caddy.Assembly()
	snapshot.Modules[0].Roles[0] = "changed"
	aSnapshot = caddy.Assembly()
	aSnapshot.Factories = nil
	aSnapshot.EventSinks = nil
	if !reflect.DeepEqual(aSnapshot, got) {
		t.Fatal("assembly retains caller slices")
	}
	cli := ctlcmd.NewCommand(ctlcmd.Options{Assembly: Validation()})
	if err := json.Unmarshal([]byte(cli.Annotations["agent-gateway.assembly"]), &got); err != nil {
		t.Fatal(err)
	}
	if got.Edition.Name != "fixture" {
		t.Fatalf("edition=%+v", got.Edition)
	}
	for _, m := range got.Modules {
		if !reflect.DeepEqual(m.Roles, []string{"validation"}) {
			t.Fatalf("CLI claims runtime role: %+v", m)
		}
	}
}

// Walk the public signature closure, including nested callback types, exported
// fields and methods. Merely compiling main does not detect hidden internal types.
func TestRequiredPublicSignaturesExposeNoCommunityInternalTypes(t *testing.T) {
	seen := map[reflect.Type]bool{}
	var check func(reflect.Type)
	check = func(typ reflect.Type) {
		if typ == nil || seen[typ] {
			return
		}
		seen[typ] = true
		if strings.HasPrefix(typ.PkgPath(), "github.com/agent-guide/agent-gateway/internal/") {
			t.Errorf("public signature exposes %s", typ)
		}
		switch typ.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Chan:
			check(typ.Elem())
		case reflect.Map:
			check(typ.Key())
			check(typ.Elem())
		case reflect.Func:
			for i := 0; i < typ.NumIn(); i++ {
				check(typ.In(i))
			}
			for i := 0; i < typ.NumOut(); i++ {
				check(typ.Out(i))
			}
		case reflect.Struct:
			for i := 0; i < typ.NumField(); i++ {
				f := typ.Field(i)
				if f.IsExported() {
					check(f.Type)
				}
			}
		}
		for i := 0; i < typ.NumMethod(); i++ {
			check(typ.Method(i).Type)
		}
	}
	for _, v := range []any{caddycmd.New, (*caddycmd.Command).Assembly, (*caddycmd.Command).Run, daemoncmd.NewCommand, daemoncmd.Run, ctlcmd.NewCommand, ctlcmd.Run, distribution.CommunityRuntime, distribution.CommunityValidation, distribution.Assembly{}, daemoncmd.Options{}, ctlcmd.Options{}, distribution.MetricsConfig{}, extension.NewRegistry, extension.NewManager, extension.Envelope{}, extension.Factory{}, extension.Snapshot{}, extension.Lease{}, extension.Error{}, extension.AdminDependencies{}, extension.AdminRoute{}, extension.OwnedAdminRoute{}, extension.WriteAdminError, (*extension.AdminContributor)(nil), event.NewDelivery, event.Validate, event.Envelope{}, event.Registration{}, event.Options{}, event.Stats{}, attribution.NewPolicy, attribution.WithContext, attribution.FromContext, attribution.WithCorrelation, attribution.Producer(nil)} {
		check(reflect.TypeOf(v))
	}
}

func TestConstructionDoesNotLoadDotenv(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("AGW_ADMIN_ADDR", "http://shell:1234")
	if err := os.WriteFile(".env.local", []byte("invalid='unterminated"), 0600); err != nil {
		t.Fatal(err)
	}
	_ = caddycmd.New(Runtime())
	_ = daemoncmd.NewCommand(daemoncmd.Options{Assembly: Runtime()})
	cli := ctlcmd.NewCommand(ctlcmd.Options{Assembly: Validation()})
	if got, _ := cli.PersistentFlags().GetString("admin-addr"); got != "http://shell:1234" {
		t.Fatalf("construction changed environment defaults: %q", got)
	}
}

func TestConsumerSourceImportsOnlyPublicCommunityPackages(t *testing.T) {
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			name, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			if strings.HasPrefix(name, "github.com/agent-guide/agent-gateway/internal/") || strings.HasPrefix(name, "github.com/agent-guide/agent-gateway/cmd/") || strings.HasPrefix(name, "github.com/agent-guide/agent-gateway-enterprise") {
				t.Errorf("%s imports forbidden %s", path, name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
