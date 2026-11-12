// Package strictjson parses bounded JSON without discarding duplicate names,
// numeric precision, malformed Unicode or trailing values.
package strictjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"
)

func Parse(data []byte, maxBytes, maxDepth int) (any, error) {
	if len(data) > maxBytes || !utf8.Valid(data) {
		return nil, fmt.Errorf("invalid JSON size or UTF-8")
	}
	// encoding/json replaces unpaired UTF-16 surrogates. Reject them before decoding.
	inString := false
	for i := 0; i < len(data); i++ {
		if data[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) {
			return nil, fmt.Errorf("invalid escape")
		}
		if data[i] != 'u' {
			continue
		}
		if i+4 >= len(data) {
			return nil, fmt.Errorf("invalid Unicode escape")
		}
		n, e := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		if e != nil {
			return nil, fmt.Errorf("invalid Unicode escape")
		}
		i += 4
		if n >= 0xDC00 && n <= 0xDFFF {
			return nil, fmt.Errorf("unpaired surrogate")
		}
		if n >= 0xD800 && n <= 0xDBFF {
			if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
				return nil, fmt.Errorf("unpaired surrogate")
			}
			lo, e := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
			if e != nil || lo < 0xDC00 || lo > 0xDFFF {
				return nil, fmt.Errorf("unpaired surrogate")
			}
			i += 6
		}
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	v, e := value(d, 0, maxDepth)
	if e != nil {
		return nil, e
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, fmt.Errorf("trailing JSON")
	}
	return v, nil
}
func value(d *json.Decoder, depth, maxDepth int) (any, error) {
	t, e := d.Token()
	if e != nil {
		return nil, e
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return t, nil
	}
	if depth >= maxDepth {
		return nil, fmt.Errorf("JSON nesting exceeded")
	}
	switch delim {
	case '{':
		m := map[string]any{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return nil, e
			}
			key, ok := k.(string)
			if !ok {
				return nil, fmt.Errorf("invalid member")
			}
			if _, ok = m[key]; ok {
				return nil, fmt.Errorf("duplicate JSON member")
			}
			v, e := value(d, depth+1, maxDepth)
			if e != nil {
				return nil, e
			}
			m[key] = v
		}
		_, e = d.Token()
		return m, e
	case '[':
		a := []any{}
		for d.More() {
			v, e := value(d, depth+1, maxDepth)
			if e != nil {
				return nil, e
			}
			a = append(a, v)
		}
		_, e = d.Token()
		return a, e
	}
	return nil, fmt.Errorf("invalid delimiter")
}
func Decode(data []byte, dst any, maxBytes, maxDepth int) error {
	if _, e := Parse(data, maxBytes, maxDepth); e != nil {
		return e
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	d.UseNumber()
	return d.Decode(dst)
}
