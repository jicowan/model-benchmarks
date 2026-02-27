package format

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
)

// OutputFormat determines how results are displayed.
type OutputFormat string

const (
	FormatTable OutputFormat = "table"
	FormatJSON  OutputFormat = "json"
	FormatCSV   OutputFormat = "csv"
)

// Table renders rows as a tab-aligned table to stdout.
func Table(headers []string, rows [][]string) {
	TableTo(os.Stdout, headers, rows)
}

// TableTo renders rows as a tab-aligned table to the given writer.
func TableTo(w io.Writer, headers []string, rows [][]string) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	// Header row.
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	// Separator.
	seps := make([]string, len(headers))
	for i, h := range headers {
		seps[i] = strings.Repeat("-", len(h))
	}
	fmt.Fprintln(tw, strings.Join(seps, "\t"))
	// Data rows.
	for _, row := range rows {
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	tw.Flush()
}

// JSON renders v as indented JSON to stdout.
func JSON(v any) error {
	return JSONTo(os.Stdout, v)
}

// JSONTo renders v as indented JSON to the given writer.
func JSONTo(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// CSV writes headers and rows as CSV to the given writer.
func CSV(w io.Writer, headers []string, rows [][]string) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(headers); err != nil {
		return err
	}
	for _, row := range rows {
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// Ptr safely dereferences a pointer, returning a formatted string or "-" if nil.
func Ptr[T any](p *T, fmtStr string) string {
	if p == nil {
		return "-"
	}
	return fmt.Sprintf(fmtStr, *p)
}

// PtrF64 formats a *float64 with the given precision, or "-" if nil.
func PtrF64(p *float64, prec int) string {
	if p == nil {
		return "-"
	}
	return fmt.Sprintf("%.*f", prec, *p)
}
