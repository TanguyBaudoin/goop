// Package vercmp implements D3's version-constraint grammar: extended
// Scoop-style ordering rather than strict SemVer, since real manifest
// versions rarely follow it (e.g. "704", "15859902", "2026.1.3.7").
// Versions are compared component-wise: numeric run against numeric run
// compares as numbers, everything else compares as a string.
package vercmp

import (
	"fmt"
	"strconv"
	"strings"
)

// Compare returns -1 if a < b, 0 if a == b, 1 if a > b.
func Compare(a, b string) int {
	as, bs := splitComponents(a), splitComponents(b)
	for i := 0; i < len(as) || i < len(bs); i++ {
		var ac, bc string
		if i < len(as) {
			ac = as[i]
		}
		if i < len(bs) {
			bc = bs[i]
		}
		if c := compareComponent(ac, bc); c != 0 {
			return c
		}
	}
	return 0
}

// splitComponents breaks a version string into alternating digit/non-digit
// runs, dropping "." and "-" separators (e.g. "10.4.2" -> ["10","4","2"],
// "15.2.0-x86_64" -> ["15","2","0","x86_64"]).
func splitComponents(v string) []string {
	var out []string
	var cur strings.Builder
	var curIsDigit bool
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range v {
		if r == '.' || r == '-' {
			flush()
			continue
		}
		isDigit := r >= '0' && r <= '9'
		if cur.Len() > 0 && isDigit != curIsDigit {
			flush()
		}
		curIsDigit = isDigit
		cur.WriteRune(r)
	}
	flush()
	return out
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func compareComponent(a, b string) int {
	if isNumeric(a) && isNumeric(b) {
		an, _ := strconv.ParseUint(a, 10, 64)
		bn, _ := strconv.ParseUint(b, 10, 64)
		switch {
		case an < bn:
			return -1
		case an > bn:
			return 1
		default:
			return 0
		}
	}
	// A missing component (empty string, from one version having fewer
	// components than the other) sorts before any real component, e.g.
	// "1.2" < "1.2.1".
	switch {
	case a == b:
		return 0
	case a == "":
		return -1
	case b == "":
		return 1
	case a < b:
		return -1
	default:
		return 1
	}
}

// Satisfies reports whether version meets constraint. constraint is an
// optional comparison operator (>=, <=, >, <, =, ==, !=) followed by a
// version; no operator means an exact match (e.g. "1.8.2" or
// ">=1.5.0" or "!=2.0.0").
func Satisfies(version, constraint string) (bool, error) {
	op, want, err := parseConstraint(constraint)
	if err != nil {
		return false, err
	}
	c := Compare(version, want)
	switch op {
	case "=", "==":
		return c == 0, nil
	case "!=":
		return c != 0, nil
	case ">":
		return c > 0, nil
	case ">=":
		return c >= 0, nil
	case "<":
		return c < 0, nil
	case "<=":
		return c <= 0, nil
	default:
		return false, fmt.Errorf("unsupported version constraint operator %q", op)
	}
}

func parseConstraint(constraint string) (op, version string, err error) {
	constraint = strings.TrimSpace(constraint)
	if constraint == "" {
		return "", "", fmt.Errorf("empty version constraint")
	}
	for _, candidate := range []string{">=", "<=", "==", "!=", ">", "<", "="} {
		if rest, ok := strings.CutPrefix(constraint, candidate); ok {
			v := strings.TrimSpace(rest)
			if v == "" {
				return "", "", fmt.Errorf("version constraint %q has an operator but no version", constraint)
			}
			return candidate, v, nil
		}
	}
	return "=", constraint, nil
}
