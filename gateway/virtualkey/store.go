package virtualkey

import "encoding/json"

// DecodeStoredVirtualKey decodes virtual key records.
func DecodeStoredVirtualKey(data []byte) (any, error) {
	var key VirtualKey
	if err := json.Unmarshal(data, &key); err != nil {
		return nil, err
	}
	if key.Key == "" {
		return nil, &json.UnmarshalTypeError{Field: "key"}
	}
	return &key, nil
}
