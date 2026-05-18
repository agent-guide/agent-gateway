package mcp

import "encoding/json"

func MarshalAny(v any) ([]byte, error) {
	return json.Marshal(v)
}

func UnmarshalAny(data []byte, dest any) error {
	return json.Unmarshal(data, dest)
}
