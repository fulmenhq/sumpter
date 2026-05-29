package dsl

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

// RunValidation executes the validation pipeline for a single extracted record.
// It returns the populated runtime or nil when validation is disabled.
func RunValidation(metadata *ValidationMetadata, record map[string]interface{}) (*ValidationRuntime, error) {
	if metadata == nil || !metadata.Enable {
		return nil, nil
	}

	if metadata.ExpressionLanguage != "" && metadata.ExpressionLanguage != "sumpter-dsl" {
		return nil, fmt.Errorf("unsupported expression language %q: only 'sumpter-dsl' is currently supported", metadata.ExpressionLanguage)
	}
	metadata.ApplyDefaults()

	runtime := NewValidationRuntime()
	metadata.Runtime = runtime
	runtime.DocumentContext = record

	if len(metadata.Accumulations) > 0 {
		accs, err := InitializeAccumulators(metadata.Accumulations)
		if err != nil {
			return nil, err
		}
		runtime.Accumulators = accs

		iterableRecords, err := getIterableRecords(metadata.ArrayPath, record)
		if err != nil {
			return nil, err
		}

		for _, item := range iterableRecords {
			if err := UpdateAllAccumulators(runtime.Accumulators, item); err != nil {
				return nil, err
			}
			runtime.RecordCount++
		}
	}

	if err := ComputeAggregations(runtime, metadata.Aggregations, record); err != nil {
		return nil, err
	}

	if err := evaluateReconciliations(runtime, metadata.Reconciliations, record); err != nil {
		return nil, err
	}

	if err := evaluateValidations(runtime, metadata.Validations, record, metadata.FailurePolicy); err != nil {
		return nil, err
	}

	if strings.TrimSpace(metadata.ArrayPath) == "" && runtime.RecordCount == 0 {
		runtime.RecordCount = 1
	}

	return runtime, nil
}

// BuildValidationReport converts the runtime into the structured metadata block that
// should be appended to extracted records.
func BuildValidationReport(metadata *ValidationMetadata, runtime *ValidationRuntime) (map[string]interface{}, error) {
	if metadata == nil || runtime == nil {
		return nil, nil
	}

	accumulatorResults, err := GetAllResults(runtime.Accumulators)
	if err != nil {
		return nil, err
	}

	placement := metadata.Placement
	if strings.TrimSpace(placement) == "" {
		placement = "footer"
	}

	report := map[string]interface{}{
		"extraction_timestamp": time.Now().UTC().Format(time.RFC3339),
		"expression_language":  metadata.ExpressionLanguage,
		"placement":            placement,
		"array_path":           metadata.ArrayPath,
		"record_count":         runtime.RecordCount,
		"accumulations":        accumulatorResults,
		"aggregations":         runtime.AggregationResults,
		"reconciliations":      runtime.ReconciliationResults,
		"validations":          runtime.ValidationResults,
		"quality_summary":      runtime.GetQualitySummary(),
	}

	return report, nil
}

func buildVariableContext(runtime *ValidationRuntime, doc map[string]interface{}) (map[string]interface{}, error) {
	variables := make(map[string]interface{})

	for name, acc := range runtime.Accumulators {
		result, err := acc.GetResult()
		if err != nil {
			return nil, fmt.Errorf("failed to get result for accumulator %s: %w", name, err)
		}
		variables[name] = result
	}

	for name, value := range runtime.AggregationResults {
		variables[name] = value
	}

	for key, value := range doc {
		variables[key] = value
	}

	for key, value := range runtime.ReconciliationScalars {
		variables[key] = value
	}

	return variables, nil
}

func ComputeAggregations(ctx *ValidationRuntime, configs []AggregationConfig, doc map[string]interface{}) error {
	for _, config := range configs {
		variables := make(map[string]interface{})

		for name, acc := range ctx.Accumulators {
			result, err := acc.GetResult()
			if err != nil {
				return fmt.Errorf("failed to get result for accumulator %s: %w", name, err)
			}
			variables[name] = result
		}

		for name, result := range ctx.AggregationResults {
			variables[name] = result
		}

		for key, value := range doc {
			variables[key] = value
		}

		expr, err := ParseExpression(config.Expression)
		if err != nil {
			return fmt.Errorf("failed to parse aggregation expression for %s: %w", config.Name, err)
		}

		evaluator := NewEvaluator(variables)
		result, err := evaluator.EvaluateExpression(expr)
		if err != nil {
			return fmt.Errorf("failed to evaluate aggregation %s: %w", config.Name, err)
		}

		ctx.AggregationResults[config.Name] = result

		if strings.TrimSpace(config.CompareTo) != "" {
			targetValue, exists := doc[config.CompareTo]
			if !exists {
				return fmt.Errorf("aggregation %s: compare_to field '%s' not found in document", config.Name, config.CompareTo)
			}

			resultFloat, err := toFloat64(result)
			if err != nil {
				return fmt.Errorf("aggregation %s: result must be numeric for comparison: %w", config.Name, err)
			}

			targetFloat, err := toFloat64(targetValue)
			if err != nil {
				return fmt.Errorf("aggregation %s: compare_to value must be numeric: %w", config.Name, err)
			}

			diff := math.Abs(resultFloat - targetFloat)
			if diff > config.Tolerance {
				return fmt.Errorf("aggregation %s: value %.6f differs from target %.6f by %.6f, exceeds tolerance %.6f",
					config.Name, resultFloat, targetFloat, diff, config.Tolerance)
			}
		}
	}

	return nil
}

func getIterableRecords(arrayPath string, record map[string]interface{}) ([]map[string]interface{}, error) {
	if strings.TrimSpace(arrayPath) == "" {
		return []map[string]interface{}{record}, nil
	}

	value := getNestedField(record, arrayPath)
	if value == nil {
		return nil, fmt.Errorf("validation array_path %q not found in extracted record", arrayPath)
	}

	switch items := value.(type) {
	case []map[string]interface{}:
		return items, nil
	case []interface{}:
		results := make([]map[string]interface{}, 0, len(items))
		for idx, elem := range items {
			m, ok := elem.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("validation array_path %q element at index %d is not an object", arrayPath, idx)
			}
			results = append(results, m)
		}
		return results, nil
	default:
		return nil, fmt.Errorf("validation array_path %q must resolve to an array of objects", arrayPath)
	}
}

func evaluateReconciliations(runtime *ValidationRuntime, configs []ReconciliationConfig, doc map[string]interface{}) error {
	if len(configs) == 0 {
		return nil
	}

	for _, config := range configs {
		variables, err := buildVariableContext(runtime, doc)
		if err != nil {
			return err
		}

		if strings.TrimSpace(config.BaseExpression) == "" {
			return fmt.Errorf("reconciliation %s requires a base_expression", config.Name)
		}

		baseExpr, err := ParseExpression(config.BaseExpression)
		if err != nil {
			return fmt.Errorf("failed to parse base_expression for reconciliation %s: %w", config.Name, err)
		}

		evaluator := NewEvaluator(variables)
		baseValueRaw, err := evaluator.EvaluateExpression(baseExpr)
		if err != nil {
			return fmt.Errorf("failed to evaluate base_expression for reconciliation %s: %w", config.Name, err)
		}

		baseValue, err := toFloat64(baseValueRaw)
		if err != nil {
			return fmt.Errorf("reconciliation %s base_expression must evaluate to a number: %w", config.Name, err)
		}

		componentResults := make([]ReconciliationComponentResult, 0, len(config.Components))
		componentTotal := 0.0

		if config.GroupBy != nil {
			groupedComponents, groupedTotal, scalarUpdates, err := evaluateGroupedComponents(config, variables, doc, baseValue)
			if err != nil {
				return err
			}

			if len(groupedComponents) > 0 {
				componentResults = append(componentResults, groupedComponents...)
				componentTotal += groupedTotal
			}

			for key, value := range scalarUpdates {
				runtime.ReconciliationScalars[key] = value
				variables[key] = value
			}

			evaluator = NewEvaluator(variables)
		}

		for _, component := range config.Components {

			if strings.TrimSpace(component.Expression) == "" {
				return fmt.Errorf("reconciliation %s component %s requires an expression", config.Name, component.Name)
			}

			expr, err := ParseExpression(component.Expression)
			if err != nil {
				return fmt.Errorf("failed to parse component expression for reconciliation %s (%s): %w", config.Name, component.Name, err)
			}

			valueRaw, err := evaluator.EvaluateExpression(expr)
			if err != nil {
				return fmt.Errorf("failed to evaluate component expression for reconciliation %s (%s): %w", config.Name, component.Name, err)
			}

			value, err := toFloat64(valueRaw)
			if err != nil {
				return fmt.Errorf("reconciliation %s component %s must evaluate to a number: %w", config.Name, component.Name, err)
			}

			componentTotal += value
			componentResults = append(componentResults, ReconciliationComponentResult{
				Name:        component.Name,
				Description: component.Description,
				Value:       value,
			})
		}

		residual := baseValue - componentTotal
		tolerance := config.Tolerance
		if tolerance == 0 {
			tolerance = 0.01
		}

		status := "balanced"
		if math.Abs(residual) > tolerance {
			if config.AllowUnexplained {
				status = "unexplained"
			} else {
				status = "unbalanced"
			}
		}

		result := ReconciliationResult{
			Name:             config.Name,
			BaseValue:        baseValue,
			Components:       componentResults,
			ComponentsTotal:  componentTotal,
			Residual:         residual,
			Tolerance:        tolerance,
			Status:           status,
			AllowUnexplained: config.AllowUnexplained,
			Severity:         config.Severity,
		}

		if strings.TrimSpace(result.Severity) == "" {
			result.Severity = "warning"
		}

		runtime.ReconciliationResults = append(runtime.ReconciliationResults, result)
		prefix := config.Name
		runtime.ReconciliationScalars[prefix+"_base"] = baseValue
		runtime.ReconciliationScalars[prefix+"_components_total"] = componentTotal
		runtime.ReconciliationScalars[prefix+"_residual"] = residual
		runtime.ReconciliationScalars[prefix+"_status"] = status
	}

	return nil
}

func evaluateValidations(runtime *ValidationRuntime, configs []ValidationConfig, doc map[string]interface{}, policy FailurePolicy) error {
	if len(configs) == 0 {
		return nil
	}

	for _, config := range configs {
		variables, err := buildVariableContext(runtime, doc)
		if err != nil {
			return err
		}

		expr, err := ParseExpression(config.Rule)
		if err != nil {
			return fmt.Errorf("failed to parse validation rule %s: %w", config.Name, err)
		}

		evaluator := NewEvaluator(variables)
		result, err := evaluator.EvaluateExpression(expr)
		if err != nil {
			return fmt.Errorf("failed to evaluate validation %s: %w", config.Name, err)
		}

		passed, ok := result.(bool)
		if !ok {
			return fmt.Errorf("validation %s did not return a boolean result", config.Name)
		}

		severity := normalizeValidationSeverity(config.Severity)
		validationResult := ValidationResult{
			Name:     config.Name,
			Severity: severity,
			Value:    result,
		}

		if passed {
			validationResult.Result = "pass"
			runtime.AddValidationResult(validationResult)
			continue
		}

		validationResult.Result = "fail"
		validationResult.Message = formatValidationMessage(config.Message, variables)
		runtime.AddValidationResult(validationResult)

		if severity == "fatal" && policy.HaltOnFirstFatal {
			break
		}
	}

	return nil
}

func formatValidationMessage(message string, variables map[string]interface{}) string {
	if message == "" {
		return ""
	}

	replacements := make([]string, 0, len(variables)*2)
	for key, value := range variables {
		replacements = append(replacements, "{"+key+"}", fmt.Sprint(value))
	}

	replacer := strings.NewReplacer(replacements...)
	return replacer.Replace(message)
}

func evaluateGroupedComponents(config ReconciliationConfig, baseVariables map[string]interface{}, doc map[string]interface{}, baseValue float64) ([]ReconciliationComponentResult, float64, map[string]interface{}, error) {
	groupCfg := config.GroupBy
	if groupCfg == nil {
		return nil, 0, nil, nil
	}

	sourcePath := strings.TrimSpace(groupCfg.Source)
	if sourcePath == "" {
		return nil, 0, nil, fmt.Errorf("reconciliation %s group_by.source is required", config.Name)
	}

	fieldPath := strings.TrimSpace(groupCfg.Field)
	if fieldPath == "" {
		return nil, 0, nil, fmt.Errorf("reconciliation %s group_by.field is required", config.Name)
	}

	valueExpression := strings.TrimSpace(groupCfg.ValueExpression)
	if valueExpression == "" {
		return nil, 0, nil, fmt.Errorf("reconciliation %s group_by.value_expression is required", config.Name)
	}

	records, err := collectGroupRecords(doc, sourcePath)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("reconciliation %s group_by source error: %w", config.Name, err)
	}

	if len(records) == 0 {
		return nil, 0, map[string]interface{}{}, nil
	}

	agg := strings.ToLower(strings.TrimSpace(groupCfg.Aggregation))
	if agg == "" {
		agg = "sum"
	}
	if agg != "sum" {
		return nil, 0, nil, fmt.Errorf("reconciliation %s group_by aggregation %q not supported", config.Name, groupCfg.Aggregation)
	}

	filterExpr, err := parseOptionalExpression(groupCfg.Filter)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("reconciliation %s group_by.filter parse error: %w", config.Name, err)
	}

	valueExpr, err := ParseExpression(valueExpression)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("reconciliation %s group_by value_expression parse error: %w", config.Name, err)
	}

	missingLabel := strings.TrimSpace(groupCfg.MissingLabel)
	if missingLabel == "" {
		missingLabel = "unknown"
	}

	totals := make(map[string]float64)
	labels := make(map[string]string)

	labelField := strings.TrimSpace(groupCfg.LabelField)

	for _, record := range records {
		context := mergeVariableContexts(baseVariables, record)
		evaluator := NewEvaluator(context)

		if filterExpr != nil {
			passed, err := evaluator.EvaluateExpression(filterExpr)
			if err != nil {
				return nil, 0, nil, fmt.Errorf("reconciliation %s group_by filter evaluation failed: %w", config.Name, err)
			}
			passedBool, ok := passed.(bool)
			if !ok {
				return nil, 0, nil, fmt.Errorf("reconciliation %s group_by filter must return boolean", config.Name)
			}
			if !passedBool {
				continue
			}
		}

		groupRaw := getNestedField(record, fieldPath)
		groupValue := resolveGroupValue(groupRaw, missingLabel)

		if labelField != "" {
			labelRaw := getNestedField(record, labelField)
			if labelRaw != nil {
				if label := strings.TrimSpace(fmt.Sprint(labelRaw)); label != "" {
					labels[groupValue] = label
				}
			}
		}

		if _, exists := labels[groupValue]; !exists {
			labels[groupValue] = groupValue
		}

		valueRaw, err := evaluator.EvaluateExpression(valueExpr)
		if err != nil {
			return nil, 0, nil, fmt.Errorf("reconciliation %s group_by value expression failed: %w", config.Name, err)
		}

		value, err := toFloat64(valueRaw)
		if err != nil {
			return nil, 0, nil, fmt.Errorf("reconciliation %s group_by value must be numeric: %w", config.Name, err)
		}

		totals[groupValue] += value
	}

	if len(totals) == 0 {
		return nil, 0, map[string]interface{}{}, nil
	}

	componentNames := make([]string, 0, len(totals))
	totalSum := 0.0
	for key, val := range totals {
		componentNames = append(componentNames, key)
		totalSum += val
	}
	sort.Strings(componentNames)

	overflowStrategy := strings.ToLower(strings.TrimSpace(groupCfg.OverflowStrategy))
	scale := 1.0
	if overflowStrategy == "cap_to_base" && totalSum > 0 {
		limit := baseValue
		if limit < 0 {
			limit = math.Abs(limit)
		}
		if limit < totalSum && limit >= 0 {
			if limit == 0 {
				scale = 0
			} else {
				scale = limit / totalSum
			}
		}
	}

	components := make([]ReconciliationComponentResult, 0, len(componentNames))
	scalarUpdates := make(map[string]interface{})
	slugCounts := make(map[string]int)
	componentMap := make(map[string]float64, len(componentNames))
	groupedTotal := 0.0

	for idx, key := range componentNames {
		rawValue := totals[key]
		value := rawValue * scale
		if math.Abs(value) < 1e-9 {
			value = 0
		}

		label := labels[key]
		name := applyTemplate(groupCfg.NameTemplate, key, label)
		if strings.TrimSpace(name) == "" {
			name = label
		}
		if strings.TrimSpace(name) == "" {
			name = fmt.Sprintf("component_%d", idx+1)
		}

		description := applyTemplate(groupCfg.DescriptionTemplate, key, label)
		if strings.TrimSpace(description) == "" {
			description = ""
		}

		components = append(components, ReconciliationComponentResult{
			Name:        name,
			Description: description,
			Value:       value,
		})

		groupedTotal += value
		componentMap[name] = value

		slug := sanitizeComponentKey(name)
		if slug == "" {
			slug = fmt.Sprintf("component_%d", idx+1)
		}
		if count := slugCounts[slug]; count > 0 {
			slug = fmt.Sprintf("%s_%d", slug, count+1)
		}
		slugCounts[slug]++

		scalarUpdates[fmt.Sprintf("%s_component_%s", config.Name, slug)] = value
	}

	scalarUpdates[fmt.Sprintf("%s_group_components_total", config.Name)] = groupedTotal
	scalarUpdates[fmt.Sprintf("%s_group_component_map", config.Name)] = componentMap

	return components, groupedTotal, scalarUpdates, nil
}

func parseOptionalExpression(expression string) (*Expression, error) {
	if strings.TrimSpace(expression) == "" {
		return nil, nil
	}
	expr, err := ParseExpression(expression)
	if err != nil {
		return nil, err
	}
	return expr, nil
}

func mergeVariableContexts(base map[string]interface{}, record map[string]interface{}) map[string]interface{} {
	merged := make(map[string]interface{}, len(base)+len(record))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range record {
		merged[key] = value
	}
	return merged
}

func resolveGroupValue(raw interface{}, missing string) string {
	if raw == nil {
		return missing
	}

	switch v := raw.(type) {
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return missing
		}
		return trimmed
	default:
		value := strings.TrimSpace(fmt.Sprint(v))
		if value == "" {
			return missing
		}
		return value
	}
}

func applyTemplate(template, groupValue, label string) string {
	template = strings.TrimSpace(template)
	if template == "" {
		return ""
	}
	result := strings.ReplaceAll(template, "{{group}}", groupValue)
	result = strings.ReplaceAll(result, "{{label}}", label)
	return result
}

var componentSlugSanitizer = regexp.MustCompile(`[^a-z0-9]+`)

func sanitizeComponentKey(name string) string {
	lowered := strings.ToLower(strings.TrimSpace(name))
	if lowered == "" {
		return ""
	}
	lowered = componentSlugSanitizer.ReplaceAllString(lowered, "_")
	lowered = strings.Trim(lowered, "_")
	return lowered
}

func collectGroupRecords(doc map[string]interface{}, path string) ([]map[string]interface{}, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("group_by source path is empty")
	}

	segments := strings.Split(path, ".")
	current := make([]interface{}, 0, 1)
	current = append(current, doc)

	for _, rawSegment := range segments {
		segment := strings.TrimSpace(rawSegment)
		if segment == "" {
			return nil, fmt.Errorf("group_by source path contains empty segment")
		}

		expectArray := strings.HasSuffix(segment, "[]")
		fieldName := strings.TrimSuffix(segment, "[]")

		next := make([]interface{}, 0)
		for _, item := range current {
			if item == nil {
				continue
			}

			obj, ok := item.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("group_by source %s expected object but found %T", path, item)
			}

			value := obj[fieldName]
			if value == nil {
				continue
			}

			if expectArray {
				switch arr := value.(type) {
				case []interface{}:
					next = append(next, arr...)
				case []map[string]interface{}:
					for _, m := range arr {
						next = append(next, m)
					}
				default:
					return nil, fmt.Errorf("group_by source %s expected array at segment %s but found %T", path, fieldName, value)
				}
			} else {
				next = append(next, value)
			}
		}
		current = next
	}

	results := make([]map[string]interface{}, 0, len(current))
	for _, item := range current {
		if item == nil {
			continue
		}
		obj, ok := item.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("group_by source %s must resolve to object, found %T", path, item)
		}
		results = append(results, obj)
	}

	return results, nil
}
