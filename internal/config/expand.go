package config

import (
	"os"
	"regexp"
	"strings"
)

// envPattern matches ${NAME}, ${NAME:-default}, and ${NAME:?message}. Bare
// $NAME and $(command) are deliberately left untouched so shell snippets in
// pipeline steps keep working.
var envPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::([-?])([^}]*))?\}`)

// expandEnv substitutes environment variables before YAML parsing. It returns
// the expanded bytes, whether any placeholder was present, and diagnostics for
// required variables that are missing.
func expandEnv(file string, raw []byte) ([]byte, bool, []Diag) {
	const sentinel = "\x00WMDEP_LITERAL_DOLLAR\x00"
	work := strings.ReplaceAll(string(raw), "$${", sentinel+"{")

	var out strings.Builder
	var diags []Diag
	templated := false
	last := 0

	for _, loc := range envPattern.FindAllStringSubmatchIndex(work, -1) {
		start, end := loc[0], loc[1]
		out.WriteString(work[last:start])

		name := work[loc[2]:loc[3]]
		op, arg := "", ""
		if loc[4] >= 0 {
			op = work[loc[4]:loc[5]]
			arg = work[loc[6]:loc[7]]
		}

		templated = true
		value, ok := os.LookupEnv(name)
		switch op {
		case "-":
			if !ok || value == "" {
				value = arg
			}
		case "?":
			if !ok || value == "" {
				msg := arg
				if msg == "" {
					msg = "required variable " + name + " is not set"
				}
				line := 1 + strings.Count(work[:start], "\n")
				diags = append(diags, Diag{File: file, Line: line, Msg: msg, Severity: "error"})
				return nil, true, diags
			}
		}

		// Keep diagnostics anchored to their original lines.
		out.WriteString(strings.ReplaceAll(value, "\n", " "))
		last = end
	}
	out.WriteString(work[last:])

	expanded := strings.ReplaceAll(out.String(), sentinel+"{", "${")
	return []byte(expanded), templated, diags
}
