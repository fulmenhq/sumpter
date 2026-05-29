package dsl

import (
	"math"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestEvaluateReconciliationsGroupByGeneratesComponents(t *testing.T) {
	record := map[string]interface{}{
		"delta": 20.0,
		"entries": []interface{}{
			map[string]interface{}{"tender": "cash", "label": "Cash", "amount": 12.0, "is_change": true},
			map[string]interface{}{"tender": "card", "label": "Card", "amount": 5.0, "is_change": true},
			map[string]interface{}{"tender": "card", "label": "Card", "amount": 3.0, "is_change": false},
		},
	}

	metadata := &ValidationMetadata{
		Enable: true,
		Reconciliations: []ReconciliationConfig{
			{
				Name:           "change",
				BaseExpression: "delta",
				Tolerance:      0.01,
				GroupBy: &ReconciliationGroupByConfig{
					Source:              "entries[]",
					Field:               "tender",
					LabelField:          "label",
					Filter:              "is_change == true",
					ValueExpression:     "amount",
					NameTemplate:        "{{group}}_change",
					DescriptionTemplate: "Change via {{label}}",
				},
				Components: []ReconciliationComponentConfig{
					{
						Name:        "unexplained",
						Description: "Remaining authorization delta",
						Expression:  "delta - change_group_components_total",
					},
				},
			},
		},
	}

	runtime, err := RunValidation(metadata, record)
	if err != nil {
		t.Fatalf("RunValidation failed: %v", err)
	}

	if runtime == nil {
		t.Fatalf("expected runtime to be populated")
	}

	if len(runtime.ReconciliationResults) != 1 {
		t.Fatalf("expected 1 reconciliation result, got %d", len(runtime.ReconciliationResults))
	}

	result := runtime.ReconciliationResults[0]
	if len(result.Components) != 3 {
		t.Fatalf("expected 3 components (2 dynamic + 1 static), got %d", len(result.Components))
	}

	cardComponent := result.Components[0]
	if cardComponent.Name != "card_change" {
		t.Fatalf("expected first component to be card_change, got %s", cardComponent.Name)
	}
	if !almostEqual(cardComponent.Value, 5.0) {
		t.Fatalf("expected card component value 5.0, got %f", cardComponent.Value)
	}
	if cardComponent.Description != "Change via Card" {
		t.Fatalf("unexpected card description: %s", cardComponent.Description)
	}

	cashComponent := result.Components[1]
	if cashComponent.Name != "cash_change" {
		t.Fatalf("expected second component to be cash_change, got %s", cashComponent.Name)
	}
	if !almostEqual(cashComponent.Value, 12.0) {
		t.Fatalf("expected cash component value 12.0, got %f", cashComponent.Value)
	}
	if cashComponent.Description != "Change via Cash" {
		t.Fatalf("unexpected cash description: %s", cashComponent.Description)
	}

	unexplained := result.Components[2]
	if unexplained.Name != "unexplained" {
		t.Fatalf("expected final component to be unexplained, got %s", unexplained.Name)
	}
	if !almostEqual(unexplained.Value, 3.0) {
		t.Fatalf("expected unexplained value 3.0, got %f", unexplained.Value)
	}

	if value, ok := runtime.ReconciliationScalars["change_component_card_change"].(float64); !ok || !almostEqual(value, 5.0) {
		t.Fatalf("expected scalar card change to be 5.0, got %#v", runtime.ReconciliationScalars["change_component_card_change"])
	}
	if value, ok := runtime.ReconciliationScalars["change_component_cash_change"].(float64); !ok || !almostEqual(value, 12.0) {
		t.Fatalf("expected scalar cash change to be 12.0, got %#v", runtime.ReconciliationScalars["change_component_cash_change"])
	}
	if value, ok := runtime.ReconciliationScalars["change_group_components_total"].(float64); !ok || !almostEqual(value, 17.0) {
		t.Fatalf("expected grouped total scalar 17.0, got %#v", runtime.ReconciliationScalars["change_group_components_total"])
	}

	if math.Abs(result.Residual) > 0.01 {
		t.Fatalf("expected residual near zero, got %f", result.Residual)
	}
}

func TestRunValidationRuleUsesTernaryExpression(t *testing.T) {
	record := map[string]interface{}{
		"widget_status": "online",
	}
	metadata := &ValidationMetadata{
		Enable: true,
		Validations: []ValidationConfig{
			{
				Name:     "friendly_status",
				Rule:     `widget_status == "online" ? true : false`,
				Severity: "error",
				Message:  "widget status should be online",
			},
		},
	}

	runtime, err := RunValidation(metadata, record)
	if err != nil {
		t.Fatalf("RunValidation failed: %v", err)
	}
	if len(runtime.ValidationResults) != 1 {
		t.Fatalf("validation results len = %d, want 1", len(runtime.ValidationResults))
	}
	if runtime.ValidationResults[0].Result != "pass" {
		t.Fatalf("validation result = %q, want pass", runtime.ValidationResults[0].Result)
	}
}

func TestRunValidationDisabledReturnsNil(t *testing.T) {
	runtime, err := RunValidation(&ValidationMetadata{Enable: false}, map[string]interface{}{"value": 1})
	if err != nil {
		t.Fatalf("RunValidation failed: %v", err)
	}
	if runtime != nil {
		t.Fatalf("runtime = %#v, want nil for disabled validation", runtime)
	}
}

func TestRunValidationRejectsUnsupportedExpressionLanguage(t *testing.T) {
	_, err := RunValidation(&ValidationMetadata{
		Enable:             true,
		ExpressionLanguage: "jsonata",
	}, map[string]interface{}{"value": 1})
	if err == nil {
		t.Fatal("RunValidation error = nil, want unsupported expression language error")
	}
}

func TestRunValidationAccumulatesArrayPathRecords(t *testing.T) {
	record := map[string]interface{}{
		"items": []interface{}{
			map[string]interface{}{"active": true, "amount": 4},
			map[string]interface{}{"active": false, "amount": 9},
			map[string]interface{}{"active": true, "amount": 6},
		},
	}
	metadata := &ValidationMetadata{
		Enable:    true,
		ArrayPath: "items",
		Accumulations: []AccumulationConfig{
			{Name: "active_count", Operation: "count", Filter: "active == true"},
			{Name: "active_amount", Operation: "sum", Field: "amount", Filter: "active == true"},
		},
		Aggregations: []AggregationConfig{
			{Name: "active_total", Expression: "active_amount"},
		},
	}

	runtime, err := RunValidation(metadata, record)
	if err != nil {
		t.Fatalf("RunValidation failed: %v", err)
	}
	if runtime.RecordCount != 3 {
		t.Fatalf("RecordCount = %d, want 3", runtime.RecordCount)
	}

	count, err := runtime.Accumulators["active_count"].GetResult()
	if err != nil {
		t.Fatalf("active_count GetResult failed: %v", err)
	}
	if count != 2 {
		t.Fatalf("active_count = %#v, want 2", count)
	}

	total, err := toFloat64(runtime.AggregationResults["active_total"])
	if err != nil {
		t.Fatalf("active_total = %#v, want numeric: %v", runtime.AggregationResults["active_total"], err)
	}
	if !almostEqual(total, 10) {
		t.Fatalf("active_total = %f, want 10", total)
	}
}

func TestValidationSeverity_OmittedSeverity_DefaultsError(t *testing.T) {
	record := map[string]interface{}{"actual": 1}
	var metadata ValidationMetadata
	if err := yaml.Unmarshal([]byte(`
enable: true
validations:
  - name: actual_matches_expected
    rule: actual == 2
    message: actual should match expected
`), &metadata); err != nil {
		t.Fatalf("yaml.Unmarshal failed: %v", err)
	}

	runtime, err := RunValidation(&metadata, record)
	if err != nil {
		t.Fatalf("RunValidation failed: %v", err)
	}

	if len(runtime.ValidationResults) != 1 {
		t.Fatalf("validation results len = %d, want 1", len(runtime.ValidationResults))
	}
	if runtime.ValidationResults[0].Severity != "error" {
		t.Fatalf("severity = %q, want error", runtime.ValidationResults[0].Severity)
	}
	summary := runtime.GetQualitySummary()
	if summary.Errors != 1 {
		t.Fatalf("Errors = %d, want 1", summary.Errors)
	}
}

func TestRunValidation_OmittedFailurePolicyFailsOnFatal(t *testing.T) {
	record := map[string]interface{}{"actual": 1}
	var metadata ValidationMetadata
	if err := yaml.Unmarshal([]byte(`
enable: true
validations:
  - name: fatal_mismatch
    rule: actual == 2
    severity: fatal
    message: fatal mismatch
`), &metadata); err != nil {
		t.Fatalf("yaml.Unmarshal failed: %v", err)
	}

	runtime, err := RunValidation(&metadata, record)
	if err != nil {
		t.Fatalf("RunValidation failed: %v", err)
	}

	shouldFail, err := runtime.ShouldFailExtraction(metadata.FailurePolicy)
	if !shouldFail {
		t.Fatalf("ShouldFailExtraction = false, want true; err=%v", err)
	}
	if err == nil {
		t.Fatal("ShouldFailExtraction error = nil, want fatal validation error")
	}
}

func TestRunValidation_ExplicitFailOnFatalFalseDoesNotFailFatal(t *testing.T) {
	record := map[string]interface{}{"actual": 1}
	var metadata ValidationMetadata
	if err := yaml.Unmarshal([]byte(`
enable: true
failure_policy:
  fail_on_fatal: false
validations:
  - name: fatal_mismatch
    rule: actual == 2
    severity: fatal
    message: fatal mismatch
`), &metadata); err != nil {
		t.Fatalf("yaml.Unmarshal failed: %v", err)
	}

	runtime, err := RunValidation(&metadata, record)
	if err != nil {
		t.Fatalf("RunValidation failed: %v", err)
	}

	shouldFail, err := runtime.ShouldFailExtraction(metadata.FailurePolicy)
	if shouldFail {
		t.Fatalf("ShouldFailExtraction = true, want false for explicit fail_on_fatal false; err=%v", err)
	}
}

func TestRunValidation_HaltOnFirstFatalDefaultsTrue(t *testing.T) {
	record := map[string]interface{}{"actual": 1}
	var metadata ValidationMetadata
	if err := yaml.Unmarshal([]byte(`
enable: true
validations:
  - name: fatal_mismatch
    rule: actual == 2
    severity: fatal
    message: fatal mismatch
  - name: skipped_after_fatal
    rule: actual == 2
    severity: error
    message: should be skipped
`), &metadata); err != nil {
		t.Fatalf("yaml.Unmarshal failed: %v", err)
	}

	runtime, err := RunValidation(&metadata, record)
	if err != nil {
		t.Fatalf("RunValidation failed: %v", err)
	}

	if len(runtime.ValidationResults) != 1 {
		t.Fatalf("validation results len = %d, want 1 due to halt_on_first_fatal default", len(runtime.ValidationResults))
	}
	if runtime.ValidationResults[0].Name != "fatal_mismatch" {
		t.Fatalf("first validation = %q, want fatal_mismatch", runtime.ValidationResults[0].Name)
	}
}

func TestRunValidation_ExplicitHaltOnFirstFatalFalseContinues(t *testing.T) {
	record := map[string]interface{}{"actual": 1}
	var metadata ValidationMetadata
	if err := yaml.Unmarshal([]byte(`
enable: true
failure_policy:
  halt_on_first_fatal: false
validations:
  - name: fatal_mismatch
    rule: actual == 2
    severity: fatal
    message: fatal mismatch
  - name: evaluated_after_fatal
    rule: actual == 2
    severity: error
    message: should be evaluated
`), &metadata); err != nil {
		t.Fatalf("yaml.Unmarshal failed: %v", err)
	}

	runtime, err := RunValidation(&metadata, record)
	if err != nil {
		t.Fatalf("RunValidation failed: %v", err)
	}

	if len(runtime.ValidationResults) != 2 {
		t.Fatalf("validation results len = %d, want 2", len(runtime.ValidationResults))
	}
}

func TestComputeAggregationsStoresResultAndComparesWithinTolerance(t *testing.T) {
	runtime := NewValidationRuntime()
	doc := map[string]interface{}{
		"reported_total": 10.005,
	}

	err := ComputeAggregations(runtime, []AggregationConfig{
		{
			Name:       "computed_total",
			Expression: "10",
			CompareTo:  "reported_total",
			Tolerance:  0.01,
		},
	}, doc)
	if err != nil {
		t.Fatalf("ComputeAggregations failed: %v", err)
	}

	got, err := toFloat64(runtime.AggregationResults["computed_total"])
	if err != nil {
		t.Fatalf("computed_total = %#v, want numeric: %v", runtime.AggregationResults["computed_total"], err)
	}
	if !almostEqual(got, 10) {
		t.Fatalf("computed_total = %f, want 10", got)
	}
}

func TestComputeAggregationsUsesDefaultTolerance(t *testing.T) {
	runtime := NewValidationRuntime()
	doc := map[string]interface{}{
		"reported_total": 10.005,
	}

	err := ComputeAggregations(runtime, []AggregationConfig{
		{
			Name:       "computed_total",
			Expression: "10",
			CompareTo:  "reported_total",
		},
	}, doc)
	if err != nil {
		t.Fatalf("ComputeAggregations failed: %v", err)
	}
}

func TestAggregationExplicitZeroToleranceRequiresExactMatch(t *testing.T) {
	var metadata ValidationMetadata
	if err := yaml.Unmarshal([]byte(`
enable: true
aggregations:
  - name: computed_total
    expression: "10"
    compare_to: reported_total
    tolerance: 0
`), &metadata); err != nil {
		t.Fatalf("yaml.Unmarshal failed: %v", err)
	}

	err := ComputeAggregations(NewValidationRuntime(), metadata.Aggregations, map[string]interface{}{
		"reported_total": 10.005,
	})
	if err == nil {
		t.Fatal("ComputeAggregations succeeded, want exact-match failure")
	}
}

func TestBuildValidationReportIncludesQualitySummary(t *testing.T) {
	metadata := &ValidationMetadata{
		Enable:             true,
		ExpressionLanguage: "sumpter-dsl",
	}
	runtime := NewValidationRuntime()
	runtime.RecordCount = 3
	runtime.AddValidationResult(ValidationResult{Result: "pass", Severity: "error"})
	runtime.AddValidationResult(ValidationResult{Result: "fail", Severity: "fatal"})

	report, err := BuildValidationReport(metadata, runtime)
	if err != nil {
		t.Fatalf("BuildValidationReport failed: %v", err)
	}

	summary, ok := report["quality_summary"].(QualitySummary)
	if !ok {
		t.Fatalf("quality_summary = %#v, want QualitySummary", report["quality_summary"])
	}
	if summary.Passed != 1 || summary.Fatals != 1 {
		t.Fatalf("quality_summary = %+v, want 1 pass and 1 fatal", summary)
	}
	if report["record_count"] != 3 {
		t.Fatalf("record_count = %#v, want 3", report["record_count"])
	}
}

func TestEvaluateReconciliationsGroupByCapsAtBase(t *testing.T) {
	record := map[string]interface{}{
		"delta_small": 10.0,
		"entries": []interface{}{
			map[string]interface{}{"tender": "cash", "amount": 12.0, "is_change": true},
			map[string]interface{}{"tender": "card", "amount": 3.0, "is_change": true},
		},
	}

	metadata := &ValidationMetadata{
		Enable: true,
		Reconciliations: []ReconciliationConfig{
			{
				Name:           "change",
				BaseExpression: "delta_small",
				Tolerance:      0.01,
				GroupBy: &ReconciliationGroupByConfig{
					Source:           "entries[]",
					Field:            "tender",
					Filter:           "is_change == true",
					ValueExpression:  "amount",
					NameTemplate:     "{{group}}",
					OverflowStrategy: "cap_to_base",
				},
				Components: []ReconciliationComponentConfig{
					{
						Name:       "remainder",
						Expression: "delta_small - change_group_components_total",
					},
				},
			},
		},
	}

	runtime, err := RunValidation(metadata, record)
	if err != nil {
		t.Fatalf("RunValidation failed: %v", err)
	}

	if runtime == nil {
		t.Fatalf("expected runtime to be populated")
	}

	if len(runtime.ReconciliationResults) != 1 {
		t.Fatalf("expected 1 reconciliation result, got %d", len(runtime.ReconciliationResults))
	}

	result := runtime.ReconciliationResults[0]
	if len(result.Components) != 3 {
		t.Fatalf("expected 3 components (2 dynamic + 1 static), got %d", len(result.Components))
	}

	card := result.Components[0]
	cash := result.Components[1]

	if !almostEqual(card.Value+cash.Value, 10.0) {
		t.Fatalf("expected dynamic components to cap at base 10.0, got %f", card.Value+cash.Value)
	}

	expectedCard := 3.0 * (10.0 / 15.0)
	expectedCash := 12.0 * (10.0 / 15.0)

	if !almostEqual(card.Value, expectedCard) {
		t.Fatalf("expected card component %.2f, got %f", expectedCard, card.Value)
	}
	if !almostEqual(cash.Value, expectedCash) {
		t.Fatalf("expected cash component %.2f, got %f", expectedCash, cash.Value)
	}

	remainder := result.Components[2]
	if !almostEqual(remainder.Value, 0.0) {
		t.Fatalf("expected remainder to be 0, got %f", remainder.Value)
	}

	if value, ok := runtime.ReconciliationScalars["change_group_components_total"].(float64); !ok || !almostEqual(value, 10.0) {
		t.Fatalf("expected capped grouped total scalar 10.0, got %#v", runtime.ReconciliationScalars["change_group_components_total"])
	}

}

func TestEvaluateReconciliationsGroupByValueExpressionUsesTernary(t *testing.T) {
	record := map[string]interface{}{
		"reported_total": 15.0,
		"lines": []interface{}{
			map[string]interface{}{"category": "widgets", "amount": 10.0, "include": true},
			map[string]interface{}{"category": "widgets", "amount": 99.0, "include": false},
			map[string]interface{}{"category": "gears", "amount": 5.0, "include": true},
		},
	}
	metadata := &ValidationMetadata{
		Enable: true,
		Reconciliations: []ReconciliationConfig{
			{
				Name:           "included_total",
				BaseExpression: "reported_total",
				Tolerance:      0.01,
				GroupBy: &ReconciliationGroupByConfig{
					Source:          "lines[]",
					Field:           "category",
					ValueExpression: "include == true ? amount : 0",
					NameTemplate:    "category_{{group}}",
				},
			},
		},
	}

	runtime, err := RunValidation(metadata, record)
	if err != nil {
		t.Fatalf("RunValidation failed: %v", err)
	}
	result := runtime.ReconciliationResults[0]
	if len(result.Components) != 2 {
		t.Fatalf("components len = %d, want 2", len(result.Components))
	}
	if !almostEqual(result.ComponentsTotal, 15.0) {
		t.Fatalf("components_total = %f, want 15", result.ComponentsTotal)
	}
	if result.Status != "balanced" {
		t.Fatalf("status = %q, want balanced", result.Status)
	}
}

func TestEvaluateReconciliationsMixedModeMergesGroupedAndStaticComponents(t *testing.T) {
	record := map[string]interface{}{
		"reported_total": 100.0,
		"lines": []interface{}{
			map[string]interface{}{"category": "widgets", "label": "Widgets", "amount": 40.0},
			map[string]interface{}{"category": "gears", "label": "Gears", "amount": 25.0},
			map[string]interface{}{"category": "gears", "label": "Gears", "amount": 10.0},
			map[string]interface{}{"category": "tools", "label": "Tools", "amount": 15.0},
		},
		"freight_adjustment":  7.0,
		"rounding_adjustment": 3.0,
	}

	metadata := &ValidationMetadata{
		Enable: true,
		Reconciliations: []ReconciliationConfig{
			{
				Name:           "order_total",
				BaseExpression: "reported_total",
				Tolerance:      0.01,
				GroupBy: &ReconciliationGroupByConfig{
					Source:              "lines[]",
					Field:               "category",
					LabelField:          "label",
					ValueExpression:     "amount",
					NameTemplate:        "category_{{group}}",
					DescriptionTemplate: "Amount for {{label}}",
				},
				Components: []ReconciliationComponentConfig{
					{
						Name:        "freight_adjustment",
						Description: "Freight adjustment",
						Expression:  "freight_adjustment",
					},
					{
						Name:        "rounding_adjustment",
						Description: "Rounding adjustment",
						Expression:  "rounding_adjustment",
					},
				},
			},
		},
	}

	runtime, err := RunValidation(metadata, record)
	if err != nil {
		t.Fatalf("RunValidation failed: %v", err)
	}
	if runtime == nil || len(runtime.ReconciliationResults) != 1 {
		t.Fatalf("expected 1 reconciliation result, got %#v", runtime)
	}

	result := runtime.ReconciliationResults[0]
	if len(result.Components) != 5 {
		t.Fatalf("expected 5 components (3 grouped + 2 static), got %d", len(result.Components))
	}

	wantOrder := []struct {
		name  string
		value float64
	}{
		{"category_gears", 35.0},
		{"category_tools", 15.0},
		{"category_widgets", 40.0},
		{"freight_adjustment", 7.0},
		{"rounding_adjustment", 3.0},
	}
	for idx, want := range wantOrder {
		got := result.Components[idx]
		if got.Name != want.name {
			t.Fatalf("component[%d].Name = %q, want %q", idx, got.Name, want.name)
		}
		if !almostEqual(got.Value, want.value) {
			t.Fatalf("component[%d].Value = %f, want %f", idx, got.Value, want.value)
		}
	}
	if !almostEqual(result.ComponentsTotal, 100.0) {
		t.Fatalf("components_total = %f, want 100", result.ComponentsTotal)
	}
	if result.Status != "balanced" {
		t.Fatalf("status = %q, want balanced", result.Status)
	}
}

// TestEvaluateReconciliationsGroupByFinancialFacts exercises the same
// group_by reconciliation surface as the retail-tender test above but
// against an XBRL-shaped record (per-period filing facts grouped by
// reporting period). The two tests together demonstrate that the DSL
// runtime is genuinely domain-neutral — the same code path handles
// both a tender breakdown and a period breakdown without special-casing.
func TestEvaluateReconciliationsGroupByFinancialFacts(t *testing.T) {
	record := map[string]interface{}{
		"period_total": 200.0,
		"facts": []interface{}{
			map[string]interface{}{"period": "Q1", "label": "Q1 2024", "value": 120.0, "is_reported": true},
			map[string]interface{}{"period": "Q2", "label": "Q2 2024", "value": 50.0, "is_reported": true},
			map[string]interface{}{"period": "Q2", "label": "Q2 2024", "value": 30.0, "is_reported": false},
		},
	}

	metadata := &ValidationMetadata{
		Enable: true,
		Reconciliations: []ReconciliationConfig{
			{
				Name:           "period",
				BaseExpression: "period_total",
				Tolerance:      0.01,
				GroupBy: &ReconciliationGroupByConfig{
					Source:              "facts[]",
					Field:               "period",
					LabelField:          "label",
					Filter:              "is_reported == true",
					ValueExpression:     "value",
					NameTemplate:        "{{group}}_reported",
					DescriptionTemplate: "Reported in {{label}}",
				},
				Components: []ReconciliationComponentConfig{
					{
						Name:        "unreported",
						Description: "Filed but not yet reported",
						Expression:  "period_total - period_group_components_total",
					},
				},
			},
		},
	}

	runtime, err := RunValidation(metadata, record)
	if err != nil {
		t.Fatalf("RunValidation failed: %v", err)
	}

	if runtime == nil {
		t.Fatalf("expected runtime to be populated")
	}

	if len(runtime.ReconciliationResults) != 1 {
		t.Fatalf("expected 1 reconciliation result, got %d", len(runtime.ReconciliationResults))
	}

	result := runtime.ReconciliationResults[0]
	if len(result.Components) != 3 {
		t.Fatalf("expected 3 components (2 dynamic + 1 static), got %d", len(result.Components))
	}

	q1Component := result.Components[0]
	if q1Component.Name != "Q1_reported" {
		t.Fatalf("expected first component to be Q1_reported, got %s", q1Component.Name)
	}
	if !almostEqual(q1Component.Value, 120.0) {
		t.Fatalf("expected Q1 component value 120.0, got %f", q1Component.Value)
	}
	if q1Component.Description != "Reported in Q1 2024" {
		t.Fatalf("unexpected Q1 description: %s", q1Component.Description)
	}

	q2Component := result.Components[1]
	if q2Component.Name != "Q2_reported" {
		t.Fatalf("expected second component to be Q2_reported, got %s", q2Component.Name)
	}
	if !almostEqual(q2Component.Value, 50.0) {
		t.Fatalf("expected Q2 component value 50.0, got %f", q2Component.Value)
	}

	unreported := result.Components[2]
	if unreported.Name != "unreported" {
		t.Fatalf("expected final component to be unreported, got %s", unreported.Name)
	}
	if !almostEqual(unreported.Value, 30.0) {
		t.Fatalf("expected unreported value 30.0, got %f", unreported.Value)
	}

	if value, ok := runtime.ReconciliationScalars["period_group_components_total"].(float64); !ok || !almostEqual(value, 170.0) {
		t.Fatalf("expected grouped total scalar 170.0, got %#v", runtime.ReconciliationScalars["period_group_components_total"])
	}

	if math.Abs(result.Residual) > 0.01 {
		t.Fatalf("expected residual near zero, got %f", result.Residual)
	}
}

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-6
}
