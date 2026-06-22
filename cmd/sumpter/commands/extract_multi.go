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
// is therefore the path-traversal containment gate for the extract-multi output
// isolation boundary. A "." or ".." id is rejected because neither is a valid
// leading-alphanumeric slug; "a..b" is allowed because, as a single path
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
// share an output directory. It is the extract-multi output-isolation preflight:
// per-recipe output isolation cannot be guaranteed if two recipes write to the
// same place, and an unvalidated id must never escape the output root.
//
// Collisions are detected case-insensitively because the team's primary
// filesystems (macOS APFS, Windows NTFS) treat "Summary" and "summary" as the
// same directory; keying the dedup on the verbatim slug would let two recipes
// silently clobber each other's output there.
func resolveRecipeOutputDirs(outputRoot string, recipeIDs []string) ([]recipeOutputDir, error) {
	if strings.TrimSpace(outputRoot) == "" {
		return nil, fmt.Errorf("extract-multi requires an output root (--output-path)")
	}
	if len(recipeIDs) == 0 {
		return nil, fmt.Errorf("extract-multi requires at least one recipe")
	}
	cleanRoot := filepath.Clean(outputRoot)
	out := make([]recipeOutputDir, 0, len(recipeIDs))
	byFoldedSlug := make(map[string]string, len(recipeIDs)) // lowercased slug -> first recipe id seen
	for _, id := range recipeIDs {
		slug, err := deriveRecipeOutputSlug(id)
		if err != nil {
			return nil, err
		}
		// Case-insensitive collision key: on a case-insensitive filesystem two
		// case-variant slugs name the same directory, so they must fail loud
		// rather than silently share (and clobber) one output directory.
		foldKey := strings.ToLower(slug)
		if prev, dup := byFoldedSlug[foldKey]; dup {
			return nil, fmt.Errorf(
				"recipes %q and %q derive output subdirectories that collide (names are compared case-insensitively for filesystem safety); extract-multi requires a distinct output subdirectory per recipe",
				prev, id)
		}
		byFoldedSlug[foldKey] = id

		dir := filepath.Join(cleanRoot, slug)
		// Lexical containment: confirm the joined directory is still under root.
		rel, err := filepath.Rel(cleanRoot, dir)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("recipe %q output subdirectory %q escapes the output root", id, slug)
		}
		// Lexical containment is not enough — filepath.Join/Rel do not follow
		// symlinks, and a pre-existing non-directory at the slug path cannot be
		// the recipe-owned output directory. Inspect any existing candidate:
		//   - a symlink (live or dangling) is refused — the downstream local
		//     writers (MkdirAll + tempfile/rename) would write through it and
		//     escape the output root (a legitimately symlinked output *root* is
		//     fine; only the recipe-owned subdir is gated);
		//   - any other non-directory (e.g. a regular file) is refused — it
		//     cannot serve as the output directory;
		//   - a pre-existing real directory is allowed (re-runs).
		if info, lerr := os.Lstat(dir); lerr == nil {
			switch {
			case info.Mode()&os.ModeSymlink != 0:
				return nil, fmt.Errorf(
					"recipe %q output subdirectory %q already exists as a symlink; refusing to write through it (extract-multi requires a real, recipe-owned output directory)",
					id, slug)
			case !info.IsDir():
				return nil, fmt.Errorf(
					"recipe %q output subdirectory %q already exists but is not a directory; extract-multi requires a real, recipe-owned output directory",
					id, slug)
			}
		} else if !errors.Is(lerr, fs.ErrNotExist) {
			return nil, fmt.Errorf("recipe %q output subdirectory %q: %w", id, slug, lerr)
		}
		out = append(out, recipeOutputDir{RecipeID: id, Slug: slug, Dir: dir})
	}
	return out, nil
}
