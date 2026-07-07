package extract

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/antchfx/xpath"
	"github.com/fulmenhq/sumpter/internal/logging"
	"go.uber.org/zap"
)

// compileXPath compiles expr, binding recipe-author prefixes to namespace URIs
// when nsMap is non-empty.
//
//   - Absent/empty map: falls back to xpath.Compile so existing recipes keep the
//     current lenient (literal-prefix) matching byte-for-byte. This is the
//     back-compat floor.
//   - Non-empty map: uses xpath.CompileWithNS, which resolves prefixes by URI and
//     fails closed (returns an error) on any prefix not bound in the map — no
//     silent 0-match from a typo'd or unbound prefix.
//
// The namespace URIs are inert match keys only; nothing here fetches, resolves,
// or networks a URI.
func compileXPath(expr string, nsMap map[string]string) (*xpath.Expr, error) {
	if len(nsMap) == 0 {
		return xpath.Compile(expr)
	}
	return xpath.CompileWithNS(expr, nsMap)
}

// validateNamespaceMap validates a recipe-author namespaces map (prefix -> URI).
// role names the config asset ("extract" / "signature") for diagnostics.
//
// Aliases are XPath prefixes (local aliases bound to a URI), not the document's
// literal prefixes: they must be non-empty, colon-free ordinary names and must
// not use the reserved xml/xmlns names. URIs must be non-empty after trimming.
// An empty alias is rejected — XPath 1.0 has no default element namespace, so a
// default-source namespace must be bound to an explicit alias such as "n".
func validateNamespaceMap(role string, nsMap map[string]string) error {
	for alias, uri := range nsMap {
		trimmed := strings.TrimSpace(alias)
		switch {
		case trimmed == "":
			return fmt.Errorf("%s namespaces: empty alias is not allowed; bind a default-source namespace to an explicit alias (e.g. n)", role)
		case trimmed != alias:
			return fmt.Errorf("%s namespaces: alias %q must not have surrounding whitespace", role, alias)
		case strings.ContainsAny(alias, ": \t"):
			return fmt.Errorf("%s namespaces: alias %q must be an ordinary non-colon name", role, alias)
		case alias == "xml" || alias == "xmlns":
			return fmt.Errorf("%s namespaces: alias %q is reserved", role, alias)
		}
		if strings.TrimSpace(uri) == "" {
			return fmt.Errorf("%s namespaces: alias %q has an empty URI", role, alias)
		}
	}
	return nil
}

// slashStepRe matches an unprefixed name immediately after a '/' step separator
// (covers //Record, /Ledger/Record, count(//Record)). leadStepRe matches an
// unprefixed name at the start of a relative expression (covers Label,
// Sub/Leaf). Both are used only to *warn*, never to fail.
var (
	slashStepRe = regexp.MustCompile(`/\s*([A-Za-z_][\w.-]*)`)
	leadStepRe  = regexp.MustCompile(`^\s*([A-Za-z_][\w.-]*)`)
)

// bareNameTests returns the distinct unprefixed element-name tests in expr. It
// is a conservative heuristic: it reports names in node-test position that carry
// no prefix, and deliberately skips prefixed names (n:Record), attribute tests
// (@id), function calls and node-type tests (count(...), text()), wildcards, and
// XPath operators. Bare tests in less common positions (e.g. inside a predicate)
// may be missed — acceptable because the result only drives an advisory warning.
func bareNameTests(expr string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(name string, endIdx int) {
		// Skip prefixed names (next char ':') and function / node-type tests
		// (next non-space char '(').
		rest := strings.TrimLeft(expr[endIdx:], " \t")
		if strings.HasPrefix(rest, ":") || strings.HasPrefix(rest, "(") {
			return
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	for _, m := range slashStepRe.FindAllStringSubmatchIndex(expr, -1) {
		// m[2],m[3] bound submatch group 1 (the name).
		add(expr[m[2]:m[3]], m[3])
	}
	if m := leadStepRe.FindStringSubmatchIndex(expr); m != nil {
		add(expr[m[2]:m[3]], m[3])
	}
	return out
}

// warnBareTestsUnderMap emits a load-time warning for each bare name test found
// in expr when a non-empty namespaces map is in scope: a bare test will not
// match fully-prefixed documents (silent under-extraction). Team decision
// 2026-07-06: warn rather than stay silent; the hard fix (optional
// default-binding) is deferred to the mode-parity work.
func warnBareTestsUnderMap(role, expr string, nsMap map[string]string) {
	if len(nsMap) == 0 {
		return
	}
	bare := bareNameTests(expr)
	if len(bare) == 0 {
		return
	}
	logger := logging.GetLogger()
	if logger == nil {
		logger = zap.NewNop()
	}
	for _, name := range bare {
		logger.Warn("bare XPath name test is unbound under a namespaces map; it will not match fully-prefixed documents",
			zap.String("config_role", role),
			zap.String("xpath", expr),
			zap.String("bare_test", name),
		)
	}
}
