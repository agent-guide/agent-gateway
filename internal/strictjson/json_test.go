package strictjson

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStrictJSON(t *testing.T) {
	for _, raw := range []string{`{"x":1,"x":2}`, `{} []`, `"\ud800"`, `"\udc00"`, string([]byte{'"', 0xff, '"'}), strings.Repeat("[", 65) + strings.Repeat("]", 65)} {
		if _, err := Parse([]byte(raw), 1<<20, 64); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	v, err := Parse([]byte(`{"n":9007199254740993,"unicode":"\ud83d\ude00"}`), 1<<20, 64)
	if err != nil {
		t.Fatal(err)
	}
	if v.(map[string]any)["n"] != json.Number("9007199254740993") {
		t.Fatal("number rounded")
	}
}
