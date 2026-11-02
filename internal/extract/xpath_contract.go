package extract

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// validateXPathContract performs token-aware load-time validation of Sumpter's
// XPath string and regular-expression function contract before or alongside
// antchfx/xpath compilation:
//
//   - Rejects arity violations that could panic or silently drop arguments
//     (e.g. lower-case/starts-with/ends-with/contains with missing args, or
//     3-arg matches / 4-arg replace with unsupported XPath 2.0 flags).
//   - Requires the pattern argument (arg 2) of matches() and replace() to be a
//     string literal and compiles it with Go's regexp.Compile (RE2) at load time.
//   - Requires the replacement argument (arg 3) of replace() to be a string
//     literal and validates its replacement template against the compiled
//     pattern's capturing groups.
func validateXPathContract(expr string) error {
	return scanXPathFunctionCalls(expr)
}

func scanXPathFunctionCalls(s string) error {
	n := len(s)
	i := 0
	for i < n {
		ch := s[i]
		// Skip single- or double-quoted string literals completely.
		if ch == '\'' || ch == '"' {
			quote := ch
			i++
			for i < n && s[i] != quote {
				i++
			}
			if i >= n {
				// Let xpath.Compile report the unclosed string literal error,
				// or return a clear error here.
				return fmt.Errorf("unclosed string literal in XPath expression %q", s)
			}
			i++
			continue
		}

		if isXPathNameStart(ch) {
			// Check that this identifier is not preceded by ':', '-', '.', '_', or a digit
			// (which would make it a continuation of an earlier QName/NCName or number).
			if i > 0 && isXPathNameContOrColon(s[i-1]) {
				i++
				continue
			}
			start := i
			i++
			for i < n && isXPathNameContOrColon(s[i]) {
				i++
			}
			ident := s[start:i]

			// Skip horizontal whitespace to see if '(' follows.
			j := i
			for j < n && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
				j++
			}
			if j < n && s[j] == '(' {
				// Avoid matching node tests after axis '::' or '@' if any, though
				// in XPath 1.0 only node-type tests have '(' after '::'.
				args, closeIdx, err := parseXPathCallArgs(s, j)
				if err != nil {
					return err
				}
				if err := checkXPathFunctionCall(ident, args); err != nil {
					return err
				}
				// Recursively scan inside each argument for nested function calls.
				for _, arg := range args {
					if err := scanXPathFunctionCalls(arg); err != nil {
						return err
					}
				}
				i = closeIdx + 1
				continue
			}
			continue
		}

		i++
	}
	return nil
}

func isXPathNameStart(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_'
}

func isXPathNameContOrColon(b byte) bool {
	return isXPathNameStart(b) || (b >= '0' && b <= '9') || b == '-' || b == '.' || b == ':'
}

// parseXPathCallArgs parses the comma-separated argument slices inside a
// function call starting at openParenIdx (where s[openParenIdx] == '(').
// It returns the raw argument substrings and the index of the matching ')'.
func parseXPathCallArgs(s string, openParenIdx int) ([]string, int, error) {
	n := len(s)
	i := openParenIdx + 1
	parenDepth := 0
	bracketDepth := 0
	argStart := i
	var args []string

	for i < n {
		ch := s[i]
		if ch == '\'' || ch == '"' {
			quote := ch
			i++
			for i < n && s[i] != quote {
				i++
			}
			if i >= n {
				return nil, -1, fmt.Errorf("unclosed string literal in XPath function arguments")
			}
			i++
			continue
		}
		switch ch {
		case '(':
			parenDepth++
		case ')':
			if parenDepth == 0 && bracketDepth == 0 {
				tail := strings.TrimSpace(s[argStart:i])
				if len(args) > 0 || tail != "" {
					args = append(args, tail)
				}
				return args, i, nil
			}
			if parenDepth > 0 {
				parenDepth--
			}
		case '[':
			bracketDepth++
		case ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		case ',':
			if parenDepth == 0 && bracketDepth == 0 {
				args = append(args, strings.TrimSpace(s[argStart:i]))
				argStart = i + 1
			}
		}
		i++
	}
	return nil, -1, fmt.Errorf("unclosed '(' in XPath function call")
}

func checkXPathFunctionCall(funcName string, args []string) error {
	for idx, a := range args {
		if a == "" {
			return fmt.Errorf("xpath %s(): empty argument at position %d", funcName, idx+1)
		}
	}

	switch funcName {
	case "lower-case":
		if len(args) != 1 {
			return fmt.Errorf("xpath lower-case() requires 1 argument (input), got %d", len(args))
		}
	case "starts-with", "ends-with", "contains", "substring-before", "substring-after":
		if len(args) != 2 {
			return fmt.Errorf("xpath %s() requires 2 arguments, got %d", funcName, len(args))
		}
	case "substring":
		if len(args) != 2 && len(args) != 3 {
			return fmt.Errorf("xpath substring() requires 2 or 3 arguments, got %d", len(args))
		}
	case "translate":
		if len(args) != 3 {
			return fmt.Errorf("xpath translate() requires 3 arguments, got %d", len(args))
		}
	case "matches":
		if len(args) != 2 {
			return fmt.Errorf("xpath matches() requires 2 arguments (input, pattern), got %d; XPath 2.0 regex flags are not supported (use inline RE2 flags such as (?i) in the pattern)", len(args))
		}
		pat, ok := unquoteXPathStringLiteral(args[1])
		if !ok {
			return fmt.Errorf("xpath matches() pattern (argument 2) must be a string literal, got %s", args[1])
		}
		if _, err := regexp.Compile(pat); err != nil {
			return fmt.Errorf("xpath matches() invalid regular expression %q: %w", pat, err)
		}
	case "replace":
		if len(args) != 3 {
			return fmt.Errorf("xpath replace() requires 3 arguments (input, pattern, replacement), got %d; XPath 2.0 regex flags are not supported (use inline RE2 flags such as (?i) in the pattern)", len(args))
		}
		pat, ok := unquoteXPathStringLiteral(args[1])
		if !ok {
			return fmt.Errorf("xpath replace() pattern (argument 2) must be a string literal, got %s", args[1])
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			return fmt.Errorf("xpath replace() invalid regular expression %q: %w", pat, err)
		}
		repl, ok := unquoteXPathStringLiteral(args[2])
		if !ok {
			return fmt.Errorf("xpath replace() replacement (argument 3) must be a string literal, got %s", args[2])
		}
		if err := validateXPathReplaceTemplate(repl, pat, re); err != nil {
			return err
		}
	}
	return nil
}

// unquoteXPathStringLiteral returns (value, true) iff raw is a single XPath 1.0
// string literal ('...' or "..."). In XPath 1.0, string literals do not have
// backslash escapes; a single-quoted literal cannot contain '\” and a
// double-quoted literal cannot contain '"'.
func unquoteXPathStringLiteral(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if len(s) < 2 {
		return "", false
	}
	quote := s[0]
	if quote != '\'' && quote != '"' {
		return "", false
	}
	if s[len(s)-1] != quote {
		return "", false
	}
	inner := s[1 : len(s)-1]
	if strings.IndexByte(inner, quote) != -1 {
		return "", false
	}
	return inner, true
}

// validateXPathReplaceTemplate validates a literal replacement string for
// XPath replace(input, pattern, replacement) against the compiled RE2 pattern.
//
// Allowed forms:
//   - Plain text without '$'
//   - '$$' for a literal '$' (except '$$<digit>' when the pattern has >=1
//     capturing groups, which antchfx/xpath v1.3.8's naive strings.ReplaceAll
//     pre-pass would rewrite to '$${<digit>}')
//   - '$0' / '${0}' (full match) and '$1'..'${N}' where 1 <= N <= re.NumSubexp()
//   - '${name}' where 'name' is a named capturing group (?P<name>...) in pattern
//
// Rejected forms:
//   - '\$' (XPath 2.0 escape not interpreted by Go's regexp.ReplaceAllString)
//   - Out-of-range group references (e.g. '$1' when NumSubexp() == 0, or '$2'
//     when NumSubexp() == 1, or '$10' when NumSubexp() < 10)
//   - Unknown named group '${foo}' or unbraced '$foo'
//   - Trailing bare '$' or unclosed '${'
func validateXPathReplaceTemplate(repl, pattern string, re *regexp.Regexp) error {
	if strings.Contains(repl, `\$`) {
		return fmt.Errorf("xpath replace() replacement %q uses XPath 2.0 '\\$' escape, which is not supported by the RE2 engine (use '$$' for a literal dollar sign)", repl)
	}

	numSubexp := re.NumSubexp()
	namedGroups := make(map[string]bool)
	for _, name := range re.SubexpNames() {
		if name != "" {
			namedGroups[name] = true
		}
	}

	i := 0
	n := len(repl)
	for i < n {
		if repl[i] != '$' {
			i++
			continue
		}
		if i+1 >= n {
			return fmt.Errorf("xpath replace() replacement %q has a trailing unescaped '$' (use '$$' for a literal dollar sign)", repl)
		}
		next := repl[i+1]
		switch {
		case next == '$':
			// Check for '$$<digit>' when the pattern has capturing groups:
			// antchfx/xpath v1.3.8 runs strings.ReplaceAll(dst, "$1", "${1}")
			// before ReplaceAllString, which would turn "$$1" into "$${1}".
			if i+2 < n && repl[i+2] >= '1' && repl[i+2] <= '9' && numSubexp > 0 {
				return fmt.Errorf("xpath replace() replacement %q cannot combine '$$' with a leading digit when pattern %q has capturing groups", repl, pattern)
			}
			i += 2
		case next == '{':
			closeIdx := strings.IndexByte(repl[i+2:], '}')
			if closeIdx < 0 {
				return fmt.Errorf("xpath replace() replacement %q has an unclosed '${'", repl)
			}
			token := repl[i+2 : i+2+closeIdx]
			if token == "" {
				return fmt.Errorf("xpath replace() replacement %q has an empty '${}'", repl)
			}
			if isAllASCIIUnsignedDigits(token) {
				grp, err := strconv.Atoi(token)
				if err != nil || grp > numSubexp {
					return fmt.Errorf("xpath replace() replacement %q references capturing group ${%s}, but pattern %q has %d capturing group(s)", repl, token, pattern, numSubexp)
				}
			} else if !namedGroups[token] {
				return fmt.Errorf("xpath replace() replacement %q references unknown named capturing group ${%s} in pattern %q", repl, token, pattern)
			}
			i = i + 2 + closeIdx + 1
		case next >= '0' && next <= '9':
			j := i + 1
			for j < n && repl[j] >= '0' && repl[j] <= '9' {
				j++
			}
			digits := repl[i+1 : j]
			grp, err := strconv.Atoi(digits)
			if err != nil || grp > numSubexp {
				return fmt.Errorf("xpath replace() replacement %q references capturing group $%s, but pattern %q has %d capturing group(s)", repl, digits, pattern, numSubexp)
			}
			i = j
		default:
			if unicode.IsLetter(rune(next)) || next == '_' {
				return fmt.Errorf("xpath replace() replacement %q has unbraced group reference starting at %q (use '${name}' for a named group or '$$' for a literal dollar sign)", repl, repl[i:])
			}
			return fmt.Errorf("xpath replace() replacement %q has invalid '$' sequence at %q (use '$$' for a literal dollar sign)", repl, repl[i:])
		}
	}
	return nil
}

func isAllASCIIUnsignedDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
