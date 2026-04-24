package api

import "strings"

// IncludeSet represents the parsed ?include=a,b,c query parameter.
// Unknown tokens are silently ignored — each handler only checks
// the tokens it knows about.
type IncludeSet map[string]bool

func parseIncludes(raw string) IncludeSet {
	if raw == "" {
		return nil
	}
	out := make(IncludeSet)
	for _, tok := range strings.Split(raw, ",") {
		if tok = strings.TrimSpace(tok); tok != "" {
			out[tok] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s IncludeSet) Has(tok string) bool { return s != nil && s[tok] }
