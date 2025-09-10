package logging

import (
	"regexp"
	"strings"
)

// PIIDetector handles detection and sanitization of PII/PHI data
type PIIDetector struct {
	mode            PIIMode
	allowedContexts []string
	patterns        []PIIPattern
}

// PIIPattern represents a PII detection pattern
type PIIPattern struct {
	Name        string
	Pattern     *regexp.Regexp
	Replacement string
	Severity    string
	Category    string // "pii" or "phi"
}

// NewPIIDetector creates a new PII detector with common patterns
func NewPIIDetector(mode PIIMode, allowedContexts []string) *PIIDetector {
	detector := &PIIDetector{
		mode:            mode,
		allowedContexts: allowedContexts,
	}

	// Initialize with comprehensive PII/PHI patterns
	detector.patterns = []PIIPattern{
		// Credit/Financial Information
		{
			Name:        "credit_card",
			Pattern:     regexp.MustCompile(`\b(?:\d{4}[-\s]?){3}\d{4}\b`),
			Replacement: "[CARD-****]",
			Severity:    "HIGH",
			Category:    "pii",
		},
		{
			Name:        "ssn",
			Pattern:     regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),
			Replacement: "[SSN-***]",
			Severity:    "CRITICAL",
			Category:    "pii",
		},
		{
			Name:        "bank_account",
			Pattern:     regexp.MustCompile(`\b\d{8,17}\b`),
			Replacement: "[ACCOUNT-***]",
			Severity:    "HIGH",
			Category:    "pii",
		},

		// Personal Information
		{
			Name:        "email",
			Pattern:     regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Z|a-z]{2,}\b`),
			Replacement: "[EMAIL-***]",
			Severity:    "MEDIUM",
			Category:    "pii",
		},
		{
			Name:        "phone",
			Pattern:     regexp.MustCompile(`\b(?:\+?1[-.\s]?)?\(?[0-9]{3}\)?[-.\s]?[0-9]{3}[-.\s]?[0-9]{4}\b`),
			Replacement: "[PHONE-***]",
			Severity:    "MEDIUM",
			Category:    "pii",
		},
		{
			Name:        "full_name",
			Pattern:     regexp.MustCompile(`\b[A-Z][a-z]+ [A-Z][a-z]+\b`),
			Replacement: "[NAME-***]",
			Severity:    "MEDIUM",
			Category:    "pii",
		},

		// Protected Health Information (PHI)
		{
			Name:        "medical_record",
			Pattern:     regexp.MustCompile(`\bMRN[-]?\d{6,10}\b`),
			Replacement: "[MRN-***]",
			Severity:    "CRITICAL",
			Category:    "phi",
		},
		{
			Name:        "health_id",
			Pattern:     regexp.MustCompile(`\b\d{10}\b`), // Generic health ID pattern
			Replacement: "[HEALTH-ID-***]",
			Severity:    "HIGH",
			Category:    "phi",
		},
		{
			Name:        "diagnosis_code",
			Pattern:     regexp.MustCompile(`\b[A-Z]\d{2}(?:\.\d{1,3})?\b`),
			Replacement: "[DX-CODE-***]",
			Severity:    "MEDIUM",
			Category:    "phi",
		},

		// Authentication & Security
		{
			Name:        "auth_header",
			Pattern:     regexp.MustCompile(`(?i)(authorization|x-api-key|cookie):\s*[^\r\n]+`),
			Replacement: "$1: [AUTH-***]",
			Severity:    "HIGH",
			Category:    "pii",
		},
		{
			Name:        "bearer_token",
			Pattern:     regexp.MustCompile(`(?i)bearer\s+[a-zA-Z0-9._-]+`),
			Replacement: "Bearer [TOKEN-***]",
			Severity:    "HIGH",
			Category:    "pii",
		},
		{
			Name:        "api_key",
			Pattern:     regexp.MustCompile(`\b[A-Za-z0-9]{32,}\b`),
			Replacement: "[API-KEY-***]",
			Severity:    "HIGH",
			Category:    "pii",
		},
		{
			Name:        "jwt_token",
			Pattern:     regexp.MustCompile(`\beyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]*\.[A-Za-z0-9_-]*\b`),
			Replacement: "[JWT-***]",
			Severity:    "HIGH",
			Category:    "pii",
		},

		// Cloud/AWS specific
		{
			Name:        "aws_key",
			Pattern:     regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
			Replacement: "[AWS-KEY-***]",
			Severity:    "CRITICAL",
			Category:    "pii",
		},
		{
			Name:        "aws_secret",
			Pattern:     regexp.MustCompile(`\b[A-Za-z0-9/+=]{40}\b`),
			Replacement: "[AWS-SECRET-***]",
			Severity:    "CRITICAL",
			Category:    "pii",
		},
	}

	return detector
}

// SanitizeData removes or masks PII/PHI from data based on current mode and context
func (d *PIIDetector) SanitizeData(data string, context string) string {
	// If PII mode is off, return data unchanged
	if d.mode == PIIModeOff {
		return data
	}

	// If context is allowed and mode is context-aware, return data unchanged
	if d.mode == PIIModeContext && d.isAllowedContext(context) {
		return data
	}

	// Default: sanitize all PII/PHI
	result := data
	for _, pattern := range d.patterns {
		result = pattern.Pattern.ReplaceAllString(result, pattern.Replacement)
	}

	return result
}

// isAllowedContext checks if the context allows PII visibility
func (d *PIIDetector) isAllowedContext(context string) bool {
	for _, allowed := range d.allowedContexts {
		if strings.Contains(context, allowed) {
			return true
		}
	}
	return false
}

// SanitizeFields sanitizes zap fields that might contain PII
func (d *PIIDetector) SanitizeFields(fields []interface{}) []interface{} {
	// For now, return fields unchanged
	// In a full implementation, this would sanitize field values
	return fields
}

// GetPIISummary returns a summary of PII patterns detected
func (d *PIIDetector) GetPIISummary(data string) map[string]int {
	summary := make(map[string]int)

	for _, pattern := range d.patterns {
		matches := pattern.Pattern.FindAllString(data, -1)
		if len(matches) > 0 {
			summary[pattern.Name] = len(matches)
		}
	}

	return summary
}
