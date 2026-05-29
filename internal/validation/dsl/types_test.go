package dsl

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestFailurePolicy_OmittedFailOnFatal_DefaultsTrue(t *testing.T) {
	var policy FailurePolicy
	if err := yaml.Unmarshal([]byte("{}"), &policy); err != nil {
		t.Fatalf("yaml.Unmarshal failed: %v", err)
	}

	policy.ApplyDefaults()

	if !policy.FailOnFatal {
		t.Fatal("FailOnFatal = false, want true for omitted field")
	}
	if !policy.HaltOnFirstFatal {
		t.Fatal("HaltOnFirstFatal = false, want true for omitted field")
	}
}

func TestFailurePolicy_ExplicitFailOnFatalFalse_RespectsOverride(t *testing.T) {
	var policy FailurePolicy
	if err := yaml.Unmarshal([]byte("fail_on_fatal: false\nhalt_on_first_fatal: false\n"), &policy); err != nil {
		t.Fatalf("yaml.Unmarshal failed: %v", err)
	}

	policy.ApplyDefaults()

	if policy.FailOnFatal {
		t.Fatal("FailOnFatal = true, want explicit false override")
	}
	if policy.HaltOnFirstFatal {
		t.Fatal("HaltOnFirstFatal = true, want explicit false override")
	}
}

func TestValidationSeverity_EmptyString_DefaultsError(t *testing.T) {
	result := ValidationResult{
		Result:   "fail",
		Severity: "",
	}
	runtime := NewValidationRuntime()
	runtime.AddValidationResult(result)

	summary := runtime.GetQualitySummary()
	if summary.Errors != 1 {
		t.Fatalf("Errors = %d, want 1 for empty severity", summary.Errors)
	}
}

func TestShouldFailExtraction_OmittedFailurePolicy_FailsOnFatal(t *testing.T) {
	runtime := NewValidationRuntime()
	runtime.AddValidationResult(ValidationResult{
		Result:   "fail",
		Severity: "fatal",
	})

	shouldFail, err := runtime.ShouldFailExtraction(FailurePolicy{})
	if !shouldFail {
		t.Fatalf("ShouldFailExtraction = false, want true; err=%v", err)
	}
	if err == nil {
		t.Fatal("ShouldFailExtraction error = nil, want fatal validation error")
	}
}

func TestSchemaRuntimeParity_FailurePolicyDefaults(t *testing.T) {
	schema := readExtractSchema(t)
	validationMetadata := mustMappingChild(t, schema, "properties", "validation_metadata", "properties")
	failurePolicy := mustMappingChild(t, validationMetadata, "failure_policy", "properties")

	policy := FailurePolicy{}
	policy.ApplyDefaults()

	assertBoolDefaultMatches(t, failurePolicy, "fail_on_fatal", policy.FailOnFatal)
	assertBoolDefaultMatches(t, failurePolicy, "halt_on_first_fatal", policy.HaltOnFirstFatal)
}

func TestSchemaRuntimeParity_ValidationSeverityDefault(t *testing.T) {
	schema := readExtractSchema(t)
	validationMetadata := mustMappingChild(t, schema, "properties", "validation_metadata", "properties")
	validations := mustMappingChild(t, validationMetadata, "validations", "items", "properties")
	severity := mustMappingChild(t, validations, "severity")
	defaultNode := mustDirectMappingChild(t, severity, "default")

	got := normalizeValidationSeverity("")
	if got != defaultNode.Value {
		t.Fatalf("runtime validation severity default = %q, schema default = %q", got, defaultNode.Value)
	}
}

func TestSchemaRuntimeParity_AggregationToleranceDefault(t *testing.T) {
	schema := readExtractSchema(t)
	validationMetadata := mustMappingChild(t, schema, "properties", "validation_metadata", "properties")
	aggregations := mustMappingChild(t, validationMetadata, "aggregations", "items", "properties")
	tolerance := mustMappingChild(t, aggregations, "tolerance")
	defaultNode := mustDirectMappingChild(t, tolerance, "default")

	var schemaDefault float64
	if err := defaultNode.Decode(&schemaDefault); err != nil {
		t.Fatalf("failed to decode aggregation tolerance default: %v", err)
	}

	config := AggregationConfig{}
	config.ApplyDefaults()
	if config.Tolerance != schemaDefault {
		t.Fatalf("runtime aggregation tolerance default = %v, schema default = %v", config.Tolerance, schemaDefault)
	}
}

func readExtractSchema(t *testing.T) *yaml.Node {
	t.Helper()

	path := filepath.Join("..", "..", "..", "schemas", "extract", "v0.1.0", "extract-record-match-schema.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read extract schema: %v", err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("failed to parse extract schema: %v", err)
	}
	if len(doc.Content) == 0 {
		t.Fatal("extract schema document is empty")
	}
	return doc.Content[0]
}

func assertBoolDefaultMatches(t *testing.T, properties *yaml.Node, field string, runtimeDefault bool) {
	t.Helper()

	fieldNode := mustMappingChild(t, properties, field)
	defaultNode := mustDirectMappingChild(t, fieldNode, "default")
	if defaultNode.Kind != yaml.ScalarNode {
		t.Fatalf("%s default node kind = %v, want scalar", field, defaultNode.Kind)
	}
	var schemaDefault bool
	if err := defaultNode.Decode(&schemaDefault); err != nil {
		t.Fatalf("failed to decode %s default: %v", field, err)
	}
	if runtimeDefault != schemaDefault {
		t.Fatalf("%s runtime default = %v, schema default = %v", field, runtimeDefault, schemaDefault)
	}
}

func mustMappingChild(t *testing.T, node *yaml.Node, path ...string) *yaml.Node {
	t.Helper()

	current := node
	for _, key := range path {
		current = mustDirectMappingChild(t, current, key)
	}
	return current
}

func mustDirectMappingChild(t *testing.T, node *yaml.Node, key string) *yaml.Node {
	t.Helper()

	if node.Kind != yaml.MappingNode {
		t.Fatalf("node for key %q kind = %v, want mapping", key, node.Kind)
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	t.Fatalf("key %q not found", key)
	return nil
}
