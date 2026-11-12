package gatewaybundle

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"gopkg.in/yaml.v3"
)

// Decode numeric nodes before yaml's interface decoder can round them through
// float64. JSON configuration semantics also reject duplicate or non-string keys.
func yamlJSONValue(n *yaml.Node) (any, error) { return yamlJSONNode(n, 0) }
func yamlJSONNode(n *yaml.Node, depth int) (any, error) {
	if depth > 128 {
		return nil, fmt.Errorf("bundle YAML nesting exceeds limit")
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) != 1 {
			return nil, fmt.Errorf("empty YAML document")
		}
		return yamlJSONNode(n.Content[0], depth+1)
	case yaml.MappingNode:
		m := map[string]any{}
		for i := 0; i < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode || k.Tag != "!!str" {
				return nil, fmt.Errorf("bundle keys must be strings")
			}
			if _, exists := m[k.Value]; exists {
				return nil, fmt.Errorf("duplicate YAML key")
			}
			v, err := yamlJSONNode(n.Content[i+1], depth+1)
			if err != nil {
				return nil, err
			}
			m[k.Value] = v
		}
		return m, nil
	case yaml.SequenceNode:
		a := []any{}
		for _, c := range n.Content {
			v, err := yamlJSONNode(c, depth+1)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		return a, nil
	case yaml.AliasNode:
		return nil, fmt.Errorf("YAML aliases unsupported in configuration")
	case yaml.ScalarNode:
		switch n.Tag {
		case "!!int", "!!float":
			s := strings.ReplaceAll(n.Value, "_", "")
			if json.Valid([]byte(s)) {
				return json.Number(s), nil
			}
			if n.Tag == "!!int" {
				if i, ok := new(big.Int).SetString(s, 0); ok {
					return json.Number(i.String()), nil
				}
			}
			return nil, fmt.Errorf("non-JSON YAML number")
		default:
			var v any
			if err := n.Decode(&v); err != nil {
				return nil, err
			}
			return v, nil
		}
	}
	return nil, fmt.Errorf("unsupported YAML node")
}
