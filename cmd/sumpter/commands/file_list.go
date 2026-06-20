package commands

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fulmenhq/sumpter/internal/uriio"
)

// maxFileListLine bounds a single file-list line so a pathological no-newline file
// cannot force an unbounded buffer allocation.
const maxFileListLine = 4 << 20 // 4 MiB

// readFileListRefs reads a newline-delimited input file list (the --file-list /
// manifest files_from input) into ordered input references. Each non-blank,
// non-comment line is one reference: a local path or an s3:// URI. This is the
// large-batch input that avoids directory enumeration entirely and the --files argv
// ceiling — the orchestrator hands sumpter exactly the file set.
//
//   - Blank lines and lines beginning with '#' are ignored.
//   - A relative LOCAL path resolves against the list file's directory (not the
//     process CWD), so a list travels with its entries.
//   - s3:// (and file://) URIs pass through verbatim, acquired through the same read
//     boundary as --files; an unsupported scheme is a loud per-line error.
//   - References are returned in listed order; an effectively empty list is an error.
func readFileListRefs(listPath string) ([]string, error) {
	abs, err := filepath.Abs(listPath)
	if err != nil {
		return nil, fmt.Errorf("--file-list %q: %w", listPath, err)
	}
	data, err := os.ReadFile(abs) // #nosec G304 - operator-provided --file-list path
	if err != nil {
		return nil, fmt.Errorf("read --file-list %q: %w", listPath, err)
	}
	baseDir := filepath.Dir(abs)

	refs := make([]string, 0, 256)
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), maxFileListLine)
	lineNum := 0
	for sc.Scan() {
		lineNum++
		entry := strings.TrimSpace(sc.Text())
		if entry == "" || strings.HasPrefix(entry, "#") {
			continue
		}
		ref, rerr := normalizeFileListRef(entry, baseDir)
		if rerr != nil {
			return nil, fmt.Errorf("--file-list %q line %d: %w", listPath, lineNum, rerr)
		}
		refs = append(refs, ref)
	}
	if serr := sc.Err(); serr != nil {
		return nil, fmt.Errorf("read --file-list %q: %w", listPath, serr)
	}
	if len(refs) == 0 {
		return nil, fmt.Errorf("--file-list %q contains no input references (only blank/comment lines)", listPath)
	}
	return refs, nil
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
