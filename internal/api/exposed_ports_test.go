package api

import (
	"reflect"
	"strings"
	"testing"
)

// TestParseExposedPorts covers the input validator that gates
// App.exposed_ports writes. The contract is intentionally strict — bad
// data lands in a DB column that Caddy reads, so we'd rather 400 a
// single PATCH than ship a stale "service-not-found" routing entry to
// the Caddyfile.
func TestParseExposedPorts(t *testing.T) {
	tests := []struct {
		name    string
		raw     *string
		want    []ExposedPort
		wantErr string // substring; "" = no error
	}{
		{"nil raw", nil, nil, ""},
		{"empty raw", strPtr(""), nil, ""},
		{"whitespace only", strPtr("   \n"), nil, ""},
		{"valid list", strPtr(`[{"name":"web","port":3000},{"name":"api","port":8080}]`),
			[]ExposedPort{{Name: "api", Port: 8080}, {Name: "web", Port: 3000}}, ""},
		{"unsorted input is sorted", strPtr(`[{"name":"b","port":1},{"name":"a","port":2}]`),
			[]ExposedPort{{Name: "a", Port: 2}, {Name: "b", Port: 1}}, ""},
		{"empty array", strPtr(`[]`), nil, ""},
		{"invalid JSON", strPtr(`not json`), nil, "invalid JSON"},
		{"missing name", strPtr(`[{"port":3000}]`), nil, "name is required"},
		{"empty name", strPtr(`[{"name":"  ","port":3000}]`), nil, "name is required"},
		{"bad name (uppercase)", strPtr(`[{"name":"Web","port":3000}]`), nil, "must match"},
		{"bad name (underscore)", strPtr(`[{"name":"my_api","port":3000}]`), nil, "must match"},
		{"name too long", strPtr(`[{"name":"` + strings.Repeat("a", 64) + `","port":3000}]`), nil, "must match"},
		{"port zero", strPtr(`[{"name":"web","port":0}]`), nil, "1..65535"},
		{"port too high", strPtr(`[{"name":"web","port":70000}]`), nil, "1..65535"},
		{"duplicate name", strPtr(`[{"name":"web","port":1},{"name":"web","port":2}]`), nil, "duplicate"},
		{"name gets trimmed", strPtr(`[{"name":"  web  ","port":3000}]`),
			[]ExposedPort{{Name: "web", Port: 3000}}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseExposedPorts(tc.raw)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (got=%v)", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %q, want substring %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestMarshalExposedPorts checks the round-trip with the column writer
// and the "empty list → NULL" contract that keeps the column NULLABLE
// rather than always carrying an empty array.
func TestMarshalExposedPorts(t *testing.T) {
	t.Run("nil/empty in → nil out", func(t *testing.T) {
		for _, in := range [][]ExposedPort{nil, {}} {
			out, err := MarshalExposedPorts(in)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if out != nil {
				t.Errorf("got %q, want nil", *out)
			}
		}
	})
	t.Run("round trip with sort", func(t *testing.T) {
		in := []ExposedPort{{Name: "b", Port: 1}, {Name: "a", Port: 2}}
		out, err := MarshalExposedPorts(in)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if out == nil {
			t.Fatal("got nil, want a JSON string")
		}
		got, err := ParseExposedPorts(out)
		if err != nil {
			t.Fatalf("re-parse: %v", err)
		}
		want := []ExposedPort{{Name: "a", Port: 2}, {Name: "b", Port: 1}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("re-parse = %+v, want %+v", got, want)
		}
	})
	t.Run("invalid input bubbles up", func(t *testing.T) {
		_, err := MarshalExposedPorts([]ExposedPort{{Name: "BAD", Port: 1}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestFindExposedPort(t *testing.T) {
	ports := []ExposedPort{{Name: "web", Port: 80}, {Name: "api", Port: 8080}}
	if findExposedPort(ports, "web") == nil {
		t.Error("expected web to be found")
	}
	if findExposedPort(ports, "missing") != nil {
		t.Error("expected missing to be nil")
	}
}
