package dataartifact

import "github.com/fulmenhq/sumpter/internal/provenance"

// Portable data-artifact/v0 lifecycle values. The contract enum is closed:
// draft | building | complete | partial | incomplete | retired.
const (
	LifecycleDraft      = "draft"
	LifecycleBuilding   = "building"
	LifecycleComplete   = "complete"
	LifecyclePartial    = "partial"
	LifecycleIncomplete = "incomplete"
	LifecycleRetired    = "retired"
)

// inputDispositionFailed mirrors the provenance input disposition wire value
// for failed inputs. Duplicated (not imported from provenance's unexported
// constant) so this package stays free of an import cycle and of a public
// surface for disposition strings that provenance already owns.
const inputDispositionFailed = "failed"

// LifecycleFromManifest maps sumpter provenance completeness signals onto the
// portable data-artifact/v0 lifecycle field. It invents no new accounting: it
// reads only Incomplete, InputsFailed, and per-input disposition values already
// present on the manifest. Descriptors are derived with LifecycleForRun, which
// adds the run's durable failure count as a second input.
//
// Precedence (first match wins):
//  1. Incomplete == true → incomplete
//     Hard failure path (for example orphaned cloud shards). A run whose
//     manifest carries incomplete:true is not successful output.
//  2. Any failed inputs → partial
//     Detected from InputsFailed > 0 when input-accounting is present, or from
//     any inputs[].disposition == "failed" (covers per-input manifests that
//     omit the optional accounting integers).
//  3. Otherwise → complete
//     applied and not_applicable inputs both count as accounted-without-failure.
//
// Reserved by the contract and not emitted by sumpter extract for finished runs:
// draft, building, retired. building is reserved for an explicitly exposed
// in-progress descriptor, which this producer profile does not emit today.
func LifecycleFromManifest(manifest provenance.Manifest) string {
	if manifest.Incomplete {
		return LifecycleIncomplete
	}
	if manifest.InputsFailed != nil && *manifest.InputsFailed > 0 {
		return LifecyclePartial
	}
	for _, input := range manifest.Inputs {
		if input.Disposition == inputDispositionFailed {
			return LifecyclePartial
		}
	}
	return LifecycleComplete
}

// LifecycleForRun is the descriptor lifecycle: LifecycleFromManifest, except that
// a complete result becomes partial when runFailed, the failed-input count from
// the run's durably written failures.json, is non-zero. An input that failed
// before it could be identified (missing or unreadable) has no manifest row, so
// without this second input such a run would read as complete. incomplete still
// takes precedence.
func LifecycleForRun(manifest provenance.Manifest, runFailed int) string {
	lifecycle := LifecycleFromManifest(manifest)
	if lifecycle == LifecycleComplete && runFailed > 0 {
		return LifecyclePartial
	}
	return lifecycle
}
