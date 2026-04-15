package config

import "strings"

// Expand substitutes {key} placeholders in s with values from vars.
// Unknown placeholders are left as-is. Simple strings.ReplaceAll is used —
// no text/template, no conditionals, no quoting.
func Expand(s string, vars map[string]string) string {
	for k, v := range vars {
		s = strings.ReplaceAll(s, "{"+k+"}", v)
	}
	return s
}

// ExpandAll applies Expand to each element of args and returns a new slice.
func ExpandAll(args []string, vars map[string]string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = Expand(a, vars)
	}
	return out
}

// BuildExec returns a single shell-ready string that runs the spawn command
// with its (already-template-expanded) args. Args containing spaces are
// single-quoted. This is the value substituted for {exec} in terminal.args.
func BuildExec(command string, args []string) string {
	parts := make([]string, 0, 1+len(args))
	parts = append(parts, command)
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

// shellQuote returns s wrapped in single quotes if it contains whitespace or
// shell-meaningful characters. Embedded single quotes are handled via the
// standard close-quote / escaped-quote / reopen-quote dance.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !needsQuoting(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func needsQuoting(s string) bool {
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' ||
			r == '"' || r == '\'' || r == '\\' ||
			r == '$' || r == '`' || r == '|' || r == '&' ||
			r == ';' || r == '<' || r == '>' || r == '(' || r == ')' {
			return true
		}
	}
	return false
}
