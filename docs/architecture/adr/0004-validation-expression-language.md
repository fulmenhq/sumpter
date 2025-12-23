# ADR-0001: Validation Expression Language for Extract Recipes

**Status:** Accepted
**Date:** 2025-09-29
**Deciders:** @3leapsdave, Polaris Navigator
**Context:** Alpha phase - validating retail extraction recipes

## Context

Sumpter's extract recipes need validation capabilities to ensure data quality during extraction. Users need to:

1. **Accumulate metrics** during extraction (e.g., count transactions, sum revenue)
2. **Aggregate data** post-extraction (e.g., verify prepay/completion pairs match)
3. **Validate results** with severity-based rules (info/warning/error/fatal)

Example use case from a retail transaction journal:

- Extract 540+ transactions including 48 fuel prepays
- Validate prepay count matches completion count (prevent double-counting)
- Verify revenue totals reconcile with journal headers
- Fail extraction if data integrity is broken

## Decision

We will implement a **custom mini-DSL** called `sumpter-dsl` for validation expressions, with provisions to add full-featured query languages later.

### Sumpter-DSL Specification v1.0

#### Supported Operations

**Accumulations** (incremental, during extraction):

```yaml
operation: "count" | "sum" | "avg" | "min" | "max"
field: "field_path"  # dot notation for nested fields
filter: "field_name op value"  # Simple comparison
```

**Filter Syntax**:

```
field_name == value          # Equality
field_name != value          # Inequality
field_name > value           # Greater than
field_name >= value          # Greater than or equal
field_name < value           # Less than
field_name <= value          # Less than or equal
field_name == null           # Null check
field_name != null           # Not null check
```

**Aggregation Expressions**:

```
variable_name                # Reference accumulation/aggregation
constant                     # Numeric or string literal
expression op expression     # Binary operations: +, -, *, /, ==, !=, <, >, <=, >=
func(expression)            # Functions: abs, count, sum
```

**Validation Rules**:

```
expression && expression     # Logical AND
expression || expression     # Logical OR
!expression                  # Logical NOT
```

#### Example

```yaml
validation_metadata:
  enable: true
  expression_language: "sumpter-dsl" # Explicit version

  accumulations:
    - name: "suspended_count"
      operation: "count"
      filter: "is_suspended == true"

    - name: "revenue_total"
      operation: "sum"
      field: "total_grand_amount"
      filter: "is_suspended == false"

  aggregations:
    - name: "revenue_match_pct"
      expression: "100 * revenue_total / total_daily_grand_amount"

  validations:
    - name: "prepay_completion_balance"
      rule: "suspended_count == completion_count"
      severity: "fatal"
      message: "Prepay/completion mismatch: {suspended_count} vs {completion_count}"
```

### Future Extension Points

The schema includes `expression_language` enum to support future additions:

```yaml
expression_language: "sumpter-dsl" | "jmespath" | "jsonata" | "cel"
```

When users need more complex queries (grouping, nested transforms, joins), we can add:

- **JMESPath** (Apache 2.0) - Good balance of power/simplicity
- **JSONata** - XPath-like power for JSON
- **CEL** - Safe evaluation for user-submitted expressions

## Rationale

### Why Custom DSL (Not JMESPath/JSONata Now)?

1. **YAGNI Principle**: Our validation needs are simple (counts, sums, comparisons)
   - 95% of use cases: `count(field where condition)` and `sum(field where condition)`
   - No grouping, pivoting, or complex transforms needed yet

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

- Grouping operations (`group_by`)
- Complex nested queries
- Data transformations/pivoting
- External tool compatibility
- User reports "DSL too limiting"

## Implementation Plan

### Phase 1: Core DSL (Alpha)

1. ✅ Add `validation_metadata` section to extract schema
2. ✅ Add `expression_language` enum field
3. ⏭️ Implement parser in `internal/validation/dsl/`
4. ⏭️ Add evaluator with accumulations/aggregations/validations
5. ⏭️ Integrate into extraction pipeline
6. ⏭️ Add comprehensive unit tests

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

- 🔄 Team needs to document DSL syntax
- 🔄 Unit tests required for parser/evaluator

## References

- [JMESPath Specification](https://jmespath.org/)
- [JSONata](https://jsonata.org/)
- [CEL Specification](https://github.com/google/cel-spec)
- Example recipes and fixtures live outside this repo (private workspaces)

## Notes

This ADR reflects Alpha phase priorities: ship working validation quickly, prove the concept, iterate based on real usage. We explicitly choose pragmatism over premature abstraction.
