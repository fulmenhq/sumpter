package dsl

import (
	"math"
	"testing"
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
