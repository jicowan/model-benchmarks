package format

import (
	"bytes"
	"strings"
	"testing"
)

func TestTableTo(t *testing.T) {
	var buf bytes.Buffer
	headers := []string{"Name", "Value"}
	rows := [][]string{
		{"ttft_p50", "12.3 ms"},
		{"throughput", "500 tok/s"},
	}
	TableTo(&buf, headers, rows)

	out := buf.String()
	if !strings.Contains(out, "Name") {
		t.Error("expected header 'Name' in output")
	}
	if !strings.Contains(out, "ttft_p50") {
		t.Error("expected row data 'ttft_p50' in output")
	}
	if !strings.Contains(out, "----") {
		t.Error("expected separator line in output")
	}
}

func TestTableTo_Empty(t *testing.T) {
	var buf bytes.Buffer
	TableTo(&buf, []string{"A", "B"}, nil)
	out := buf.String()
	// Should still have headers and separator.
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines (header+separator), got %d", len(lines))
	}
}

func TestJSONTo(t *testing.T) {
	var buf bytes.Buffer
	data := map[string]string{"hello": "world"}
	if err := JSONTo(&buf, data); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, `"hello": "world"`) {
		t.Errorf("unexpected JSON output: %s", out)
	}
}

func TestCSV(t *testing.T) {
	var buf bytes.Buffer
	headers := []string{"col1", "col2"}
	rows := [][]string{{"a", "b"}, {"c", "d"}}
	if err := CSV(&buf, headers, rows); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Errorf("expected 3 CSV lines, got %d", len(lines))
	}
	if lines[0] != "col1,col2" {
		t.Errorf("unexpected header: %s", lines[0])
	}
}

func TestPtrF64(t *testing.T) {
	val := 123.456
	if got := PtrF64(&val, 1); got != "123.5" {
		t.Errorf("PtrF64(&123.456, 1) = %q, want %q", got, "123.5")
	}
	if got := PtrF64(nil, 2); got != "-" {
		t.Errorf("PtrF64(nil, 2) = %q, want %q", got, "-")
	}
}

func TestPtr(t *testing.T) {
	val := 42
	if got := Ptr(&val, "%d"); got != "42" {
		t.Errorf("Ptr(&42, %%d) = %q, want %q", got, "42")
	}
	if got := Ptr[int](nil, "%d"); got != "-" {
		t.Errorf("Ptr(nil, %%d) = %q, want %q", got, "-")
	}
}
