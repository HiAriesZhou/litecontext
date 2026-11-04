package strictjson

import "testing"

func TestDecodeRejectsInvalidContracts(t *testing.T) {
	type value struct {
		Version int      `json:"version"`
		Items   []string `json:"items"`
	}
	for _, data := range []string{`{"version":1,"version":2,"items":[]}`, `{"version":1}`, `{"version":1,"items":null}`, `{"version":1,"items":[],"unknown":true}`, `{"version":1,"items":[]} {}`, `{"version":true,"items":[]}`, `null`} {
		var v value
		if Decode([]byte(data), &v) == nil {
			t.Fatalf("accepted %s", data)
		}
	}
	var v value
	if err := Decode([]byte(`{"version":1,"items":[]}`), &v); err != nil {
		t.Fatal(err)
	}
}
