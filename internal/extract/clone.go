package extract

import (
	"sync"

	"github.com/fulmenhq/sumpter/internal/validation/dsl"
)

func cloneExtractConfigForStreaming(cfg *ExtractRecordMatch) *ExtractRecordMatch {
	clone := cloneExtractConfig(cfg)
	if clone == nil {
		return nil
	}
	clone.MatchSelectors = []MatchSelector{{XPath: "/*"}}
	return clone
}

// CloneRecordMatch returns a deep copy of cfg with all compiled XPath / match-XPath
// state cleared (so each holder compiles and owns its own *xpath.Expr) while the
// run-scoped, immutable reference-table registry stays shared BY POINTER (never
// deep-copied or reloaded). Parallel extraction gives each worker its own clone so no
// two workers share mutable XPath evaluator state, while the load-once registry and
// read-only external fields remain shared. It covers match selectors, field mappings,
// nested item_mapping, and polymorphic match_xpath + nested fields.
func CloneRecordMatch(cfg *ExtractRecordMatch) *ExtractRecordMatch {
	return cloneExtractConfig(cfg)
}

func cloneExtractConfig(cfg *ExtractRecordMatch) *ExtractRecordMatch {
	if cfg == nil {
		return nil
	}

	clone := ExtractRecordMatch{
		RecordType:         cfg.RecordType,
		MatchSelectors:     cloneMatchSelectors(cfg.MatchSelectors),
		OutputSchema:       cloneStringInterfaceMap(cfg.OutputSchema),
		FieldMappings:      cloneFieldMappings(cfg.FieldMappings),
		Filters:            cloneStringInterfaceMap(cfg.Filters),
		ValidationMetadata: cloneValidationMetadata(cfg.ValidationMetadata),
		OutputOptions:      cfg.OutputOptions,
		Summaries:          cloneSummaryConfigs(cfg.Summaries),
		UniformSchema:      cfg.UniformSchema,
		// ReferenceTables is the run-scoped, immutable reference-table registry,
		// shared by pointer across clones — including the streaming path's
		// cloneExtractConfigForStreaming — so reference expressions resolve on
		// every extraction path. Nothing reachable from it is mutated after build, so
		// sharing the same registry across concurrent workers is safe without copying.
		ReferenceTables: cfg.ReferenceTables,
		prepareOnce:     sync.Once{},
	}
	if cfg.OutputOptions != nil {
		opts := *cfg.OutputOptions
		clone.OutputOptions = &opts
	}
	return &clone
}

func cloneMatchSelectors(selectors []MatchSelector) []MatchSelector {
	if selectors == nil {
		return nil
	}
	clone := make([]MatchSelector, len(selectors))
	for i := range selectors {
		clone[i] = selectors[i]
		clone[i].Attributes = cloneStringInterfaceMap(selectors[i].Attributes)
		clone[i].CompiledXPath = nil
	}
	return clone
}

func cloneFieldMappings(mappings []FieldMapping) []FieldMapping {
	if mappings == nil {
		return nil
	}
	clone := make([]FieldMapping, len(mappings))
	for i := range mappings {
		clone[i] = mappings[i]
		clone[i].TransformParams = cloneStringInterfaceMap(mappings[i].TransformParams)
		clone[i].ItemMapping = cloneFieldMappings(mappings[i].ItemMapping)
		clone[i].Polymorphic = clonePolymorphicMappings(mappings[i].Polymorphic)
		clone[i].CompiledXPath = nil
	}
	return clone
}

func clonePolymorphicMappings(mappings []PolymorphicMapping) []PolymorphicMapping {
	if mappings == nil {
		return nil
	}
	clone := make([]PolymorphicMapping, len(mappings))
	for i := range mappings {
		clone[i] = mappings[i]
		clone[i].FieldMappings = cloneFieldMappings(mappings[i].FieldMappings)
		clone[i].CompiledMatchXPath = nil
	}
	return clone
}

func cloneSummaryConfigs(summaries []SummaryConfig) []SummaryConfig {
	if summaries == nil {
		return nil
	}
	clone := make([]SummaryConfig, len(summaries))
	for i := range summaries {
		clone[i] = summaries[i]
		if summaries[i].Components != nil {
			clone[i].Components = append([]SummaryComponentConfig(nil), summaries[i].Components...)
		}
	}
	return clone
}

func cloneValidationMetadata(metadata *dsl.ValidationMetadata) *dsl.ValidationMetadata {
	if metadata == nil {
		return nil
	}
	clone := *metadata
	clone.Runtime = nil
	clone.Accumulations = append([]dsl.AccumulationConfig(nil), metadata.Accumulations...)
	clone.Aggregations = append([]dsl.AggregationConfig(nil), metadata.Aggregations...)
	clone.Validations = append([]dsl.ValidationConfig(nil), metadata.Validations...)
	clone.Reconciliations = cloneReconciliationConfigs(metadata.Reconciliations)
	return &clone
}

func cloneReconciliationConfigs(configs []dsl.ReconciliationConfig) []dsl.ReconciliationConfig {
	if configs == nil {
		return nil
	}
	clone := make([]dsl.ReconciliationConfig, len(configs))
	for i := range configs {
		clone[i] = configs[i]
		if configs[i].Components != nil {
			clone[i].Components = append([]dsl.ReconciliationComponentConfig(nil), configs[i].Components...)
		}
		if configs[i].GroupBy != nil {
			groupBy := *configs[i].GroupBy
			clone[i].GroupBy = &groupBy
		}
	}
	return clone
}

func cloneStringInterfaceMap(in map[string]interface{}) map[string]interface{} {
	if in == nil {
		return nil
	}
	clone := make(map[string]interface{}, len(in))
	for key, value := range in {
		clone[key] = value
	}
	return clone
}
