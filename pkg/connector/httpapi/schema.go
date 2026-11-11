package httpapi

import (
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Schema is compiled once at publication. Validation preserves mathematical
// numeric equality, including integers beyond IEEE-754's exact range.
type Schema struct {
	raw        map[string]any
	types      map[string]bool
	properties map[string]*Schema
	items      *Schema
}

func CompileSchema(raw map[string]any) (*Schema, error) {
	b, e := json.Marshal(raw)
	if e != nil || len(b) > 64<<10 {
		return nil, fmt.Errorf("schema exceeds 64 KiB")
	}
	return compileSchema(raw, 1, true)
}
func compileSchema(raw map[string]any, depth int, root bool) (*Schema, error) {
	if depth > 32 {
		return nil, fmt.Errorf("schema depth exceeded")
	}
	s := &Schema{raw: raw, types: map[string]bool{}, properties: map[string]*Schema{}}
	add := func(v any) error {
		t, ok := v.(string)
		if !ok {
			return fmt.Errorf("invalid schema type")
		}
		switch t {
		case "object", "array", "string", "number", "integer", "boolean", "null":
		default:
			return fmt.Errorf("unsupported schema type")
		}
		if s.types[t] {
			return fmt.Errorf("duplicate type")
		}
		s.types[t] = true
		return nil
	}
	switch t := raw["type"].(type) {
	case string:
		if e := add(t); e != nil {
			return nil, e
		}
	case []any:
		if len(t) == 0 {
			return nil, fmt.Errorf("empty types")
		}
		for _, t := range t {
			if e := add(t); e != nil {
				return nil, e
			}
		}
	default:
		return nil, fmt.Errorf("schema type required")
	}
	if root && (len(s.types) != 1 || !s.types["object"]) {
		return nil, fmt.Errorf("schema root must be object")
	}
	for k, v := range raw {
		if v == nil {
			return nil, fmt.Errorf("null schema keyword")
		}
		switch k {
		case "type":
		case "title", "description":
			if _, ok := v.(string); !ok {
				return nil, fmt.Errorf("invalid annotation")
			}
		case "properties":
			if !s.types["object"] {
				return nil, fmt.Errorf("properties require object")
			}
			m, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid properties")
			}
			for n, v := range m {
				r, ok := v.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("invalid property schema")
				}
				c, e := compileSchema(r, depth+1, false)
				if e != nil {
					return nil, e
				}
				s.properties[n] = c
			}
		case "required":
			if !s.types["object"] {
				return nil, fmt.Errorf("required needs object")
			}
			a, ok := v.([]any)
			if !ok {
				return nil, fmt.Errorf("invalid required")
			}
			seen := map[string]bool{}
			for _, v := range a {
				n, ok := v.(string)
				if !ok || seen[n] {
					return nil, fmt.Errorf("invalid required member")
				}
				seen[n] = true
			}
		case "additionalProperties":
			if !s.types["object"] {
				return nil, fmt.Errorf("additionalProperties needs object")
			}
			if _, ok := v.(bool); !ok {
				return nil, fmt.Errorf("additionalProperties must be boolean")
			}
		case "items":
			if !s.types["array"] {
				return nil, fmt.Errorf("items require array")
			}
			r, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("items must be schema")
			}
			c, e := compileSchema(r, depth+1, false)
			if e != nil {
				return nil, e
			}
			s.items = c
		case "minLength", "maxLength", "minItems", "maxItems":
			t := "string"
			if k == "minItems" || k == "maxItems" {
				t = "array"
			}
			if !s.types[t] {
				return nil, fmt.Errorf("bound incompatible with type")
			}
			n, ok := number(v)
			if !ok || !n.IsInt() || n.Sign() < 0 {
				return nil, fmt.Errorf("invalid nonnegative bound")
			}
		case "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum":
			if !s.types["number"] && !s.types["integer"] {
				return nil, fmt.Errorf("numeric bound incompatible with type")
			}
			if _, ok := number(v); !ok {
				return nil, fmt.Errorf("invalid numeric bound")
			}
		case "enum":
			a, ok := v.([]any)
			if !ok || len(a) == 0 {
				return nil, fmt.Errorf("invalid enum")
			}
		default:
			return nil, fmt.Errorf("unsupported schema keyword %q", k)
		}
	}
	if s.types["array"] && s.items == nil {
		return nil, fmt.Errorf("array items required")
	}
	if a, ok := raw["required"].([]any); ok {
		for _, v := range a {
			if s.properties[v.(string)] == nil {
				return nil, fmt.Errorf("required property not declared")
			}
		}
	}
	for _, p := range [][2]string{{"minLength", "maxLength"}, {"minItems", "maxItems"}} {
		a, aok := number(raw[p[0]])
		b, bok := number(raw[p[1]])
		if aok && bok && a.Cmp(b) > 0 {
			return nil, fmt.Errorf("contradictory bounds")
		}
	}
	for _, l := range []string{"minimum", "exclusiveMinimum"} {
		for _, u := range []string{"maximum", "exclusiveMaximum"} {
			a, aok := number(raw[l])
			b, bok := number(raw[u])
			if aok && bok && (a.Cmp(b) > 0 || a.Cmp(b) == 0 && (l == "exclusiveMinimum" || u == "exclusiveMaximum")) {
				return nil, fmt.Errorf("contradictory numeric bounds")
			}
		}
	}
	if s.types["integer"] && !s.types["number"] && len(s.types) == 1 {
		for _, lo := range []string{"minimum", "exclusiveMinimum"} {
			for _, hi := range []string{"maximum", "exclusiveMaximum"} {
				l, lok := number(raw[lo])
				h, hok := number(raw[hi])
				if lok && hok {
					lower := integerBound(l, true, lo == "exclusiveMinimum")
					upper := integerBound(h, false, hi == "exclusiveMaximum")
					if lower != nil && upper != nil && lower.Cmp(upper) > 0 {
						return nil, fmt.Errorf("integer interval has no values")
					}
				}
			}
		}
	}
	if a, ok := raw["enum"].([]any); ok {
		for i, v := range a {
			if e := s.validate(v, false); e != nil {
				return nil, fmt.Errorf("enum violates schema")
			}
			for _, prev := range a[:i] {
				if equal(prev, v) {
					return nil, fmt.Errorf("duplicate enum value")
				}
			}
		}
	}
	return s, nil
}

type decimal struct {
	sign     int
	digits   string
	exponent big.Int
}

func (n *decimal) Sign() int   { return n.sign }
func (n *decimal) IsInt() bool { return n.sign == 0 || n.exponent.Sign() >= 0 }
func (n *decimal) Cmp(other *decimal) int {
	if n.sign != other.sign {
		if n.sign < other.sign {
			return -1
		}
		return 1
	}
	if n.sign == 0 {
		return 0
	}
	var a, b big.Int
	a.Add(&n.exponent, big.NewInt(int64(len(n.digits))))
	b.Add(&other.exponent, big.NewInt(int64(len(other.digits))))
	c := a.Cmp(&b)
	if c == 0 {
		max := len(n.digits)
		if len(other.digits) > max {
			max = len(other.digits)
		}
		for i := 0; i < max; i++ {
			x, y := byte('0'), byte('0')
			if i < len(n.digits) {
				x = n.digits[i]
			}
			if i < len(other.digits) {
				y = other.digits[i]
			}
			if x < y {
				c = -1
				break
			}
			if x > y {
				c = 1
				break
			}
		}
	}
	return c * n.sign
}
func number(v any) (*decimal, bool) {
	raw, ok := v.(json.Number)
	if !ok {
		return nil, false
	}
	text := string(raw)
	if !json.Valid([]byte(text)) {
		return nil, false
	}
	n := &decimal{sign: 1}
	if strings.HasPrefix(text, "-") {
		n.sign = -1
		text = text[1:]
	}
	if i := strings.IndexAny(text, "eE"); i >= 0 {
		if _, ok := n.exponent.SetString(text[i+1:], 10); !ok {
			return nil, false
		}
		text = text[:i]
	}
	if i := strings.IndexByte(text, '.'); i >= 0 {
		n.exponent.Sub(&n.exponent, big.NewInt(int64(len(text)-i-1)))
		text = text[:i] + text[i+1:]
	}
	text = strings.TrimLeft(text, "0")
	if text == "" {
		n.sign = 0
		n.digits = "0"
		n.exponent.SetInt64(0)
		return n, true
	}
	trimmed := strings.TrimRight(text, "0")
	n.exponent.Add(&n.exponent, big.NewInt(int64(len(text)-len(trimmed))))
	n.digits = trimmed
	return n, true
}
func equal(a, b any) bool {
	if x, ok := number(a); ok {
		y, ok := number(b)
		return ok && x.Cmp(y) == 0
	}
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			w, ok := y[k]
			if !ok || !equal(v, w) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i, v := range x {
			if !equal(v, y[i]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(a, b)
}
func (s *Schema) Validate(v any) error { return s.validate(v, true) }
func (s *Schema) validate(v any, enum bool) error {
	t := ""
	switch v.(type) {
	case nil:
		t = "null"
	case string:
		t = "string"
	case bool:
		t = "boolean"
	case map[string]any:
		t = "object"
	case []any:
		t = "array"
	case json.Number:
		t = "number"
	default:
		return fmt.Errorf("invalid JSON value")
	}
	matches := s.types[t]
	if n, ok := number(v); ok && n.IsInt() && s.types["integer"] {
		matches = true
	}
	if !matches {
		return fmt.Errorf("argument type mismatch")
	}
	if enum {
		if a, ok := s.raw["enum"].([]any); ok {
			found := false
			for _, x := range a {
				if equal(v, x) {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("argument outside enum")
			}
		}
	}
	switch x := v.(type) {
	case map[string]any:
		if a, ok := s.raw["required"].([]any); ok {
			for _, v := range a {
				if _, ok := x[v.(string)]; !ok {
					return fmt.Errorf("required argument missing")
				}
			}
		}
		for k, v := range x {
			if c := s.properties[k]; c != nil {
				if e := c.Validate(v); e != nil {
					return e
				}
			} else if s.raw["additionalProperties"] == false {
				return fmt.Errorf("unknown argument")
			}
		}
	case []any:
		if e := s.length(len(x), "minItems", "maxItems"); e != nil {
			return e
		}
		for _, v := range x {
			if e := s.items.Validate(v); e != nil {
				return e
			}
		}
	case string:
		if e := s.length(utf8.RuneCountInString(x), "minLength", "maxLength"); e != nil {
			return e
		}
	case json.Number:
		n, ok := number(x)
		if !ok {
			return fmt.Errorf("invalid number")
		}
		for _, k := range []string{"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum"} {
			b, ok := number(s.raw[k])
			if !ok {
				continue
			}
			cmp := n.Cmp(b)
			if k == "minimum" && cmp < 0 || k == "maximum" && cmp > 0 || k == "exclusiveMinimum" && cmp <= 0 || k == "exclusiveMaximum" && cmp >= 0 {
				return fmt.Errorf("number outside bound")
			}
		}
	}
	return nil
}
func (s *Schema) length(n int, min, max string) error {
	v, _ := number(json.Number(strconv.Itoa(n)))
	if b, ok := number(s.raw[min]); ok && v.Cmp(b) < 0 {
		return fmt.Errorf("length below minimum")
	}
	if b, ok := number(s.raw[max]); ok && v.Cmp(b) > 0 {
		return fmt.Errorf("length above maximum")
	}
	return nil
}

func integerBound(n *decimal, lower, exclusive bool) *decimal {
	var whole big.Int
	if n.exponent.Sign() >= 0 {
		if !n.exponent.IsInt64() || n.exponent.Int64() > 65536 {
			return nil
		}
		whole.SetString(n.digits+strings.Repeat("0", int(n.exponent.Int64())), 10)
		if n.sign < 0 {
			whole.Neg(&whole)
		}
		if exclusive {
			if lower {
				whole.Add(&whole, big.NewInt(1))
			} else {
				whole.Sub(&whole, big.NewInt(1))
			}
		}
	} else {
		var magnitude big.Int
		magnitude.Neg(&n.exponent)
		cut := 0
		if magnitude.IsInt64() && magnitude.Int64() < int64(len(n.digits)) {
			cut = len(n.digits) - int(magnitude.Int64())
		}
		if cut > 0 {
			whole.SetString(n.digits[:cut], 10)
		}
		if n.sign < 0 {
			whole.Neg(&whole)
		}
		if lower && n.sign > 0 {
			whole.Add(&whole, big.NewInt(1))
		}
		if !lower && n.sign < 0 {
			whole.Sub(&whole, big.NewInt(1))
		}
	}
	v, _ := number(json.Number(whole.String()))
	return v
}
