package commands

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// recipeSlugPattern is the portable-slug rule for an extract-multi per-recipe
// output subdirectory. It mirrors the credential-handle slug discipline
// (internal/uriio handleNamePattern): a leading alphanumeric followed by
// alphanumerics, dot, underscore, or hyphen.
//
// This is deliberately strict because extract-multi derives each recipe's output
// directory as <output-root>/<recipe-id>/, and the recipe manifest validates
// `id` only as non-empty — it imposes no charset constraint, so an `id` could
// otherwise carry a path separator, a parent-directory ("..") segment, an
// absolute/volume prefix, or a control character into the output root. The slug
// is therefore the path-traversal containment gate for the output isolation
// boundary (SUM-057 SF1). A "." or ".." id is rejected because neither is a
// valid leading-alphanumeric slug; "a..b" is allowed because, as a single path
// component, it is an ordinary directory name and not a parent reference.
var recipeSlugPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// deriveRecipeOutputSlug validates a recipe id for use as an output
// subdirectory name and returns it verbatim as a slug (no normalization, so the
// slug maps 1:1 to the id and two distinct ids can never silently collide). The
// returned slug is guaranteed to be a single, contained path component.
func deriveRecipeOutputSlug(recipeID string) (string, error) {
	if strings.TrimSpace(recipeID) == "" {
		return "", fmt.Errorf("recipe id is empty; cannot derive an output subdirectory")
	}
	// Reject (do not normalize) leading/trailing whitespace: the slug is used
	// verbatim, so a normalized id would map an output directory to a value that
	// differs from the recipe/provenance identity — an avoidable ambiguity at
	// the per-recipe output boundary.
	if recipeID != strings.TrimSpace(recipeID) {
		return "", fmt.Errorf(
			"recipe id %q has leading or trailing whitespace; it must be a clean slug so the output subdirectory maps 1:1 to the recipe identity",
			recipeID)
	}
	if !recipeSlugPattern.MatchString(recipeID) {
		return "", fmt.Errorf(
			"recipe id %q is not usable as an output subdirectory name (allowed: %s); "+
				"rename the recipe id so extract-multi can write <output-root>/<id>/ safely",
			recipeID, recipeSlugPattern.String())
	}
	// Defense in depth (safeStagePath discipline): a pattern-valid id must
	// still be a single path component with no separator or volume prefix.
	if strings.ContainsAny(recipeID, `/\`) || filepath.VolumeName(recipeID) != "" || recipeID != filepath.Base(recipeID) {
		return "", fmt.Errorf("recipe id %q does not resolve to a single output path component", recipeID)
	}
	return recipeID, nil
}

// recipeOutputDir is a resolved, validated output destination for one recipe in
// an extract-multi run.
type recipeOutputDir struct {
	RecipeID string // the recipe id (verbatim, for error messages)
	Slug     string // validated single-component slug
	Dir      string // <output-root>/<slug>, confirmed contained under the root
}

// resolveRecipeOutputDirs derives and validates a contained output directory for
// each recipe under outputRoot, and fails loud — before any input is read,
// parsed, or written — if a recipe id escapes the root or two recipes would
// share an output directory. It is the SF1/SF5 preflight for extract-multi: per
// recipe output isolation cannot be guaranteed if two recipes write to the same
// place, and an unvalidated id must never escape the output root (SUM-057
// SF1/SF5).
func resolveRecipeOutputDirs(outputRoot string, recipeIDs []string) ([]recipeOutputDir, error) {
	if strings.TrimSpace(outputRoot) == "" {
		return nil, fmt.Errorf("extract-multi requires an output root (--output-path)")
	}
	if len(recipeIDs) == 0 {
		return nil, fmt.Errorf("extract-multi requires at least one recipe")
	}
	cleanRoot := filepath.Clean(outputRoot)
	out := make([]recipeOutputDir, 0, len(recipeIDs))
	bySlug := make(map[string]string, len(recipeIDs)) // slug -> first recipe id seen
	for _, id := range recipeIDs {
		slug, err := deriveRecipeOutputSlug(id)
		if err != nil {
			return nil, err
		}
		if prev, dup := bySlug[slug]; dup {
			return nil, fmt.Errorf(
				"recipes %q and %q derive the same output subdirectory %q; extract-multi requires a distinct output subdirectory per recipe",
				prev, id, slug)
		}
		bySlug[slug] = id

		dir := filepath.Join(cleanRoot, slug)
		// Lexical containment: confirm the joined directory is still under root.
		rel, err := filepath.Rel(cleanRoot, dir)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("recipe %q output subdirectory %q escapes the output root", id, slug)
		}
		// SF1: lexical containment is not enough — filepath.Join/Rel do not
		// follow symlinks. If the recipe's output subdirectory already exists as
		// a symlink, the downstream local writers (MkdirAll + tempfile/rename)
		// would write through it, escaping the output root. Refuse to write into
		// a pre-existing symlinked recipe directory (a legitimately symlinked
		// output *root* is fine — only the recipe-owned subdir is gated). This
		// catches both live and dangling symlink targets.
		if info, lerr := os.Lstat(dir); lerr == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf(
					"recipe %q output subdirectory %q already exists as a symlink; refusing to write through it (extract-multi requires a real, recipe-owned output directory)",
					id, slug)
			}
		} else if !errors.Is(lerr, fs.ErrNotExist) {
			return nil, fmt.Errorf("recipe %q output subdirectory %q: %w", id, slug, lerr)
		}
		out = append(out, recipeOutputDir{RecipeID: id, Slug: slug, Dir: dir})
	}
	return out, nil
}
