package localapikey

import "encoding/json"

// DecodeStoredLocalAPIKey decodes local API key records.
func DecodeStoredLocalAPIKey(data []byte) (any, error) {
	var key LocalAPIKey
	if err := json.Unmarshal(data, &key); err != nil {
		return nil, err
	}
	if key.Key == "" {
		return nil, &json.UnmarshalTypeError{Field: "key"}
	}
	return &key, nil
}
