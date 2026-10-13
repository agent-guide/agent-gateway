package schema

import (
	"encoding/json"
	"fmt"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/extension"
)

var ExtensionSchema = configstore.StoreSchema{
	Name: StoreExtensions, Kind: "extension", Table: "extensions", PrimaryKeyColumns: []string{"module_id"}, DataColumn: "config", Timestamped: false,
	Codec: typedJSONCodec{kind: "extension", decode: func(data []byte) (any, error) {
		var e extension.Envelope
		if err := json.Unmarshal(data, &e); err != nil {
			return nil, err
		}
		return &e, nil
	}, validate: func(obj any) error {
		var e extension.Envelope
		switch v := unwrapConfigObject(obj).(type) {
		case extension.Envelope:
			e = v
		case *extension.Envelope:
			if v == nil {
				return fmt.Errorf("nil extension")
			}
			e = *v
		default:
			return fmt.Errorf("invalid extension object")
		}
		if !extension.ValidID(e.ModuleID) || e.SchemaVersion <= 0 {
			return fmt.Errorf("invalid stored extension envelope")
		}
		_, err := extension.DecodeObject(e.Config)
		return err
	}}, Metadata: configstore.MetadataFuncs{PrimaryKeyFunc: primaryKeyFromStringFields("ModuleID")},
}
