package provenance

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// NewRunID returns a UUIDv7 run identifier suitable for lexicographic
// time-ordering in output paths and manifests.
func NewRunID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate UUIDv7 run id: %w", err)
	}
	return id.String(), nil
}

// ValidateRunID validates an explicit replay/testing run id.
func ValidateRunID(candidate string) error {
	_, err := normalizeRunID(candidate)
	return err
}

func normalizeRunID(candidate string) (string, error) {
	id, err := uuid.Parse(strings.TrimSpace(candidate))
	if err != nil {
		return "", fmt.Errorf("parse run id: %w", err)
	}
	if id == uuid.Nil {
		return "", fmt.Errorf("run id must not be nil UUID")
	}
	if id.Version() != uuid.Version(7) {
		return "", fmt.Errorf("run id must be UUIDv7, got version %s", id.Version())
	}
	if id.Variant() != uuid.RFC4122 {
		return "", fmt.Errorf("run id must use RFC4122 variant, got %s", id.Variant())
	}
	return id.String(), nil
}

// ResolveRunID returns the explicit CLI run id, the environment run id, or a
// newly generated UUIDv7 in that precedence order.
func ResolveRunID(flagValue, envValue string) (string, error) {
	for _, candidate := range []string{strings.TrimSpace(flagValue), strings.TrimSpace(envValue)} {
		if candidate == "" {
			continue
		}
		runID, err := normalizeRunID(candidate)
		if err != nil {
			return "", err
		}
		return runID, nil
	}
	return NewRunID()
}
