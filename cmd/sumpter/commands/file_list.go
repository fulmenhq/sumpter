package commands

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/fulmenhq/sumpter/internal/uriio"
)

// maxFileListLine bounds a single file-list line so a pathological no-newline file
// cannot force an unbounded buffer allocation.
const maxFileListLine = 4 << 20 // 4 MiB

// fileListSHA256Pattern is the canonical digest spelling for an integrity-bound
// object line: sha256: plus 64 lowercase hex. A bare 64-hex digest is accepted
// and normalized to the prefixed form. Uppercase hex fails closed (accepted
// forms are exactly the ones the manifest ledger emits).
var fileListSHA256Pattern = regexp.MustCompile(`^(sha256:)?[0-9a-f]{64}$`)

// fileListDeclaration is one integrity-bound {uri,size,sha256} object line from a
// --file-list. URI is the normalized reference (same classification and
// list-relative resolution as a URI-only line); Size is a non-negative byte
// count; SHA256 is the normalized sha256:<hex> digest.
type fileListDeclaration struct {
	URI    string
	Size   int64
	SHA256 string
}

// fileListEntry is one reference from a file list. Ref is always the normalized
// reference (local path or s3:// / file:// URI). Declaration is non-nil only for
// object lines, which bind the input to declared content identity; declarations
// ride the entry's ordinal position, never a path-keyed map, so duplicate
// references cannot cross-apply an expected digest.
type fileListEntry struct {
	Ref         string
	Declaration *fileListDeclaration
}

// readFileListRefs reads a newline-delimited input file list (the --file-list /
// manifest files_from input) into ordered input entries. Each non-blank,
// non-comment line is one entry:
//
//   - A URI-only line (today's form) is a local path or an s3:// / file:// URI.
//   - A line whose first non-space character is '{' is one JSON object line that
//     declares content identity: {"uri":..., "size":..., "sha256":...}. Unknown
//     fields (including version_id in this cut) fail closed.
//
// Order is preserved and mixed URI-only / object lines are allowed, so
// unbound and integrity-bound inputs can share one list. Blank lines and lines
// beginning with '#' are ignored. A relative LOCAL path (from either form)
// resolves against the list file's directory (not the process CWD), so a list
// travels with its entries. s3:// and file:// URIs pass through verbatim,
// acquired through the same read boundary as --files; an unsupported scheme is a
// loud per-line error. An effectively empty list is an error.
func readFileListRefs(listPath string) ([]fileListEntry, error) {
	abs, err := filepath.Abs(listPath)
	if err != nil {
		return nil, fmt.Errorf("--file-list %q: %w", listPath, err)
	}
	data, err := os.ReadFile(abs) // #nosec G304 - operator-provided --file-list path
	if err != nil {
		return nil, fmt.Errorf("read --file-list %q: %w", listPath, err)
	}
	baseDir := filepath.Dir(abs)

	entries := make([]fileListEntry, 0, 256)
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), maxFileListLine)
	lineNum := 0
	for sc.Scan() {
		lineNum++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line[0] == '{' {
			decl, derr := parseFileListObjectLine(line, baseDir)
			if derr != nil {
				return nil, fmt.Errorf("--file-list %q line %d: %w", listPath, lineNum, derr)
			}
			entries = append(entries, fileListEntry{Ref: decl.URI, Declaration: &decl})
			continue
		}
		ref, rerr := normalizeFileListRef(line, baseDir)
		if rerr != nil {
			return nil, fmt.Errorf("--file-list %q line %d: %w", listPath, lineNum, rerr)
		}
		entries = append(entries, fileListEntry{Ref: ref})
	}
	if serr := sc.Err(); serr != nil {
		return nil, fmt.Errorf("read --file-list %q: %w", listPath, serr)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("--file-list %q contains no input references (only blank/comment lines)", listPath)
	}
	return entries, nil
}

// parseFileListObjectLine strictly decodes one integrity-bound object line.
// Every deviation fails closed: duplicate keys, unknown keys, missing required
// fields, null/non-string/non-integer values, fractional/exponent/negative/
// overflowing sizes, malformed or truncated JSON, and trailing content after
// the object. The recovered declaration carries the normalized reference.
func parseFileListObjectLine(line, baseDir string) (fileListDeclaration, error) {
	var decl fileListDeclaration

	dec := json.NewDecoder(strings.NewReader(line))
	dec.UseNumber()

	tok, err := dec.Token()
	if err != nil {
		return decl, fmt.Errorf("invalid object line: %w", err)
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return decl, fmt.Errorf("object line must be a single JSON object")
	}

	seen := make(map[string]bool, 3)
	var (
		uri      string
		size     int64
		sha      string
		haveURI  bool
		haveSize bool
		haveSHA  bool
	)
	for dec.More() {
		keyTok, kerr := dec.Token()
		if kerr != nil {
			return decl, fmt.Errorf("invalid object line: %w", kerr)
		}
		key, ok := keyTok.(string)
		if !ok {
			return decl, fmt.Errorf("object line has a non-string key")
		}
		if seen[key] {
			return decl, fmt.Errorf("duplicate field %q", key)
		}
		seen[key] = true
		switch key {
		case "uri":
			v, verr := dec.Token()
			if verr != nil {
				return decl, fmt.Errorf("invalid uri field: %w", verr)
			}
			s, ok := v.(string)
			if !ok || strings.TrimSpace(s) == "" {
				return decl, fmt.Errorf("uri must be a non-empty string")
			}
			uri = s
			haveURI = true
		case "size":
			v, verr := dec.Token()
			if verr != nil {
				return decl, fmt.Errorf("invalid size field: %w", verr)
			}
			num, ok := v.(json.Number)
			if !ok {
				return decl, fmt.Errorf("size must be a non-negative integer byte count")
			}
			literal := num.String()
			if !isDecimalDigits(literal) {
				return decl, fmt.Errorf("size must be a non-negative integer byte count (got %q)", literal)
			}
			parsed, perr := strconv.ParseInt(literal, 10, 64)
			if perr != nil {
				return decl, fmt.Errorf("size out of range (got %q)", literal)
			}
			size = parsed
			haveSize = true
		case "sha256":
			v, verr := dec.Token()
			if verr != nil {
				return decl, fmt.Errorf("invalid sha256 field: %w", verr)
			}
			s, ok := v.(string)
			if !ok {
				return decl, fmt.Errorf("sha256 must be a string")
			}
			if !fileListSHA256Pattern.MatchString(s) {
				return decl, fmt.Errorf("sha256 must be 64 lowercase hex digits with an optional sha256: prefix")
			}
			if !strings.HasPrefix(s, "sha256:") {
				s = "sha256:" + s
			}
			sha = s
			haveSHA = true
		default:
			return decl, fmt.Errorf("unknown field %q", key)
		}
	}
	// Consume the closing brace.
	if _, cerr := dec.Token(); cerr != nil {
		return decl, fmt.Errorf("invalid object line: %w", cerr)
	}
	// Exactly one object per line: any further token (object, array, scalar, or
	// malformed fragment) is trailing content and fails closed.
	if trailing, terr := dec.Token(); terr != io.EOF {
		if terr != nil {
			return decl, fmt.Errorf("trailing content after object: %w", terr)
		}
		return decl, fmt.Errorf("trailing content after object: %v", trailing)
	}

	if !haveURI {
		return decl, fmt.Errorf("uri is required")
	}
	if !haveSize {
		return decl, fmt.Errorf("size is required")
	}
	if !haveSHA {
		return decl, fmt.Errorf("sha256 is required")
	}

	ref, rerr := normalizeFileListRef(uri, baseDir)
	if rerr != nil {
		return decl, rerr
	}
	return fileListDeclaration{URI: ref, Size: size, SHA256: sha}, nil
}

// isDecimalDigits reports whether s is a non-empty run of ASCII digits (no sign,
// fraction, exponent, whitespace, or empty string), so a JSON size that is not a
// plain non-negative integer literal fails closed.
func isDecimalDigits(s string) bool {
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

// normalizeFileListRef classifies one list entry and validates its scheme. Any URI
// form (s3://, file://) passes through verbatim — the uriio read boundary resolves it,
// exactly as for --files entries. Only a bare local path is normalized: a relative one
// resolves against the list file's directory, an absolute one is left as-is. An
// unsupported scheme is a loud error (the caller adds line context).
func normalizeFileListRef(entry, baseDir string) (string, error) {
	if _, err := uriio.Classify(entry); err != nil {
		return "", err
	}
	// uriio treats any entry containing "://" as a scheme-bearing URI (s3:// or
	// file://); those flow to the read boundary unchanged. Bare paths have no scheme.
	if strings.Contains(entry, "://") {
		return entry, nil
	}
	if !filepath.IsAbs(entry) {
		return filepath.Join(baseDir, entry), nil
	}
	return entry, nil
}
