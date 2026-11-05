package bundle

import "testing"

func TestRejectDuplicateKeys(t *testing.T) {
	accepted := []string{
		`{}`,
		`{"a":1,"b":{"c":"quote \" and brace }"}}`,
		`{"visibility":"private","confidence":"high"}`,
		`{"\u0062":1,"a":2}`,
		`[1,{"a":true},{"a":false}]`,
		" \n{\"a\":null} ",
	}
	for _, value := range accepted {
		if err := rejectDuplicateKeys([]byte(value)); err != nil {
			t.Fatalf("%s: %v", value, err)
		}
	}
	rejected := []string{
		`{"a":1,"a":2}`,
		`{"a":1,"b":{"a":1,"a":2}}`,
		`{"\u0061":1,"a":2}`,
		`{"a":1}{"b":2}`,
		`{"a":1,}`,
		`{"visibility":""}`,
		`{"confidence":""}`,
	}
	for _, value := range rejected {
		if err := rejectDuplicateKeys([]byte(value)); err == nil {
			t.Fatalf("accepted %s", value)
		}
	}
}
