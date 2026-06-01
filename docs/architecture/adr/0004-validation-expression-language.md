# ADR-0004: Validation Expression Language for Extract Recipes

**Status:** Accepted
**Date:** 2025-09-29
**Deciders:** @3leapsdave (with `devlead` / `qa` AI contribution)
**Context:** Alpha phase — validating extraction recipes for record-based XML formats

## Context

Sumpter's extract recipes need validation capabilities to ensure data quality during extraction. Users need to:

1. **Accumulate metrics** during extraction (e.g., count records, sum measured values)
2. **Aggregate data** post-extraction (e.g., verify per-category sums reconcile with reported totals)
3. **Validate results** with severity-based rules (info/warning/error/fatal)

Example use case — ClinVar variant-archive extraction:

- Extract several thousand `VariationArchive` records, each carrying a clinical-significance classification
- Accumulate per-classification counts during extraction
- Aggregate against the release-level summary the corpus declares
- Fail extraction if the per-classification counts don't reconcile with the declared totals

## Decision

We will implement a **custom mini-DSL** called `sumpter-dsl` for validation
expressions, with provisions to add full-featured query languages later.

The current grammar, function set, operator precedence, filter semantics, type
rules, and parser behavior contracts are maintained in the canonical
[Sumpter DSL Reference](../../dsl-reference.md). This ADR records why Sumpter
uses a custom mini-DSL and when that decision should be revisited; the
reference document records what the DSL does today.

Specification note: `sumpter-dsl` includes string functions (`lower`, `upper`,
`normalize_space`, `mask_tail`, `mask_middle`) and records the string-function
nil-propagation contract in the DSL reference.

### Example

```yaml
validation_metadata:
  enable: true
  expression_language: "sumpter-dsl"

  accumulations:
    - name: "active_count"
      operation: "count"
      filter: "is_active == true"

    - name: "total_amount"
      operation: "sum"
      field: "amount"
      filter: "is_active == true"

  aggregations:
    - name: "amount_match_pct"
      expression: "100 * total_amount / reported_total"

  validations:
    - name: "count_balance"
      rule: "active_count == expected_count"
      severity: "fatal"
      message: "Count mismatch: {active_count} vs {expected_count}"
```

### Future Extension Points

The schema includes `expression_language` enum to support future additions:

```yaml
expression_language: "sumpter-dsl" | "jmespath" | "jsonata" | "cel"
```

When users need more complex queries beyond the current DSL surface, we can add:

- **JMESPath** (Apache 2.0) - Good balance of power/simplicity
- **JSONata** - XPath-like power for JSON
- **CEL** - Safe evaluation for user-submitted expressions

## Rationale

### Why Custom DSL (Not JMESPath/JSONata Now)?

1. **YAGNI Principle**: Our validation needs are simple (counts, sums, comparisons)
   - 95% of use cases: `count(field where condition)` and `sum(field where condition)`
   - No pivoting or complex transforms needed yet

2. **Implementation Speed**:
   - Mini-DSL: ~200 lines, 2-4 hours implementation
   - JMESPath integration: ~4-6 hours learning + integration
   - Can ship validation TODAY vs next week

3. **Maintainability**:
   - Team can read/maintain simple Go parser
   - No external library dependency churn
   - Clear, purpose-built for validation

4. **User Experience**:
   - Dead simple syntax for common cases
   - Lower barrier to writing recipes
   - Fewer edge cases to document

5. **Evolution Path**:
   - Expressions are isolated in config
   - Can add JMESPath alongside DSL
   - Not locked in - can migrate incrementally

### Why Not JSONPath?

- No native aggregations (sum, avg, count)
- Would require external processing anyway
- Weak on complex expressions

### Why Not CEL?

- Not designed for path-based querying
- Aggregations require workarounds
- Overkill for our validation needs
- Better for safe eval of user expressions (future use case)

### When to Reconsider?

Add JMESPath/JSONata when we need:

- Grouping operations beyond the current reconciliation `group_by` surface
- Complex nested queries
- Data transformations/pivoting
- External tool compatibility
- User reports "DSL too limiting"

## Implementation Plan

### Phase 1: Core DSL (Alpha)

1. ✅ Add `validation_metadata` section to extract schema
2. ✅ Add `expression_language` enum field
3. ✅ Implement parser in `internal/validation/dsl/`
4. ✅ Add evaluator with accumulations/aggregations/validations
5. ✅ Integrate into extraction pipeline
6. ✅ Add comprehensive unit tests

### Phase 2: Enhancement (Beta/Production)

- Add more functions (e.g., `median`, `stddev`, `percentile`)
- Support array operations
- Add string functions if needed
- Consider JMESPath if limitations emerge

### File Structure

```
internal/validation/
  dsl/
    parser.go        # Parse filter/expression strings
    evaluator.go     # Evaluate expressions
    accumulator.go   # Incremental operations
    types.go         # Data structures
    parser_test.go
    evaluator_test.go
```

## Consequences

### Positive

- ✅ Fast time to value (validation working today)
- ✅ Simple, readable syntax for common cases
- ✅ Easy to maintain and debug
- ✅ No external dependencies
- ✅ Clear migration path to full query languages

### Negative

- ⚠️ Limited expressiveness (by design)
- ⚠️ Will need to add features over time
- ⚠️ May hit limitations requiring JMESPath later

### Neutral

- DSL syntax is documented in the [Sumpter DSL Reference](../../dsl-reference.md)
- Unit tests remain required for parser/evaluator changes

## References

- [JMESPath Specification](https://jmespath.org/)
- [JSONata](https://jsonata.org/)
- [CEL Specification](https://github.com/google/cel-spec)
- [Sumpter DSL Reference](../../dsl-reference.md)
- Example recipes and fixtures live outside this repo (private workspaces)

## Notes

This ADR reflects Alpha phase priorities: ship working validation quickly, prove the concept, iterate based on real usage. We explicitly choose pragmatism over premature abstraction.
