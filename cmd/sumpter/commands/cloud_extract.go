package commands

import (
	"fmt"
	"strings"

	"github.com/fulmenhq/sumpter/internal/uriio"
)

const (
	cloudInputModeEager   = "eager"
	cloudInputModeBounded = "bounded"
)

func normalizeCloudInputMode(mode string) string {
	m := strings.ToLower(strings.TrimSpace(mode))
	if m == "" {
		return cloudInputModeEager
	}
	return m
}

func boundedCloudInput(opts *ExtractOptions) bool {
	return opts != nil && normalizeCloudInputMode(opts.CloudInputMode) == cloudInputModeBounded
}

func validateCloudInputOptions(opts *ExtractOptions) error {
	if opts == nil {
		return nil
	}
	mode := normalizeCloudInputMode(opts.CloudInputMode)
	switch mode {
	case cloudInputModeEager:
		return nil
	case cloudInputModeBounded:
		if strings.TrimSpace(opts.FileList) == "" {
			return fmt.Errorf("--cloud-input-mode bounded requires --file-list (URI-list input)")
		}
		if opts.CloudStagingMaxBytes <= 0 {
			return fmt.Errorf("--cloud-input-mode bounded requires --cloud-staging-max-bytes > 0")
		}
		if opts.CloudStagingMaxFiles <= 0 {
			return fmt.Errorf("--cloud-input-mode bounded requires --cloud-staging-max-files > 0")
		}
		if opts.CloudObjectMaxBytes <= 0 {
			return fmt.Errorf("--cloud-input-mode bounded requires --cloud-object-max-bytes > 0")
		}
		if opts.CloudPrefetch < 0 {
			return fmt.Errorf("--cloud-prefetch must be >= 0")
		}
		return nil
	default:
		return fmt.Errorf("--cloud-input-mode must be %q or %q (got %q)", cloudInputModeEager, cloudInputModeBounded, opts.CloudInputMode)
	}
}

func cloudPrefetchWindow(opts *ExtractOptions, inputWorkers int) int {
	if opts == nil {
		return 1
	}
	if opts.CloudPrefetch > 0 {
		return opts.CloudPrefetch
	}
	if inputWorkers < 1 {
		inputWorkers = 1
	}
	return inputWorkers
}

func attachStagingBudget(session *uriio.Session, opts *ExtractOptions) {
	if session == nil || !boundedCloudInput(opts) {
		return
	}
	session.SetStagingBudget(uriio.NewStagingBudget(uriio.StagingBudgetConfig{
		MaxBytes:  opts.CloudStagingMaxBytes,
		MaxFiles:  opts.CloudStagingMaxFiles,
		ObjectMax: opts.CloudObjectMaxBytes,
	}))
}
