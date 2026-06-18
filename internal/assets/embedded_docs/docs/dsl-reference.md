# Sumpter DSL Reference

**Reference version:** Sumpter DSL reference v1.3
**Runtime language value:** `sumpter-dsl`
**Status:** Current recipe-author reference for v0.1.4

This document is the canonical reference for the Sumpter expression language
used by extract recipes. It describes the grammar and runtime behavior that
recipe authors can rely on today. The reference version above is documentation
metadata only; Sumpter does not currently accept a runtime declaration such as
`expression_language: sumpter-dsl@v1.3`.

## Where the DSL Is Used

Sumpter DSL expressions appear in these recipe surfaces:

| Surface                   | Field                                                                    | Expected result                                      |
| ------------------------- | ------------------------------------------------------------------------ | ---------------------------------------------------- |
| Scalar field mappings     | `field_mappings[].expression`                                            | Value compatible with the declared output field type |
| Validation aggregations   | `validation_metadata.aggregations[].expression`                          | Scalar value                                         |
| Reconciliation totals     | `validation_metadata.reconciliations[].base_expression`                  | Numeric value                                        |
| Reconciliation components | `validation_metadata.reconciliations[].components[].expression`          | Numeric value                                        |
| Reconciliation grouping   | `validation_metadata.reconciliations[].group_by.filter`                  | Boolean value                                        |
| Reconciliation grouping   | `validation_metadata.reconciliations[].group_by.value_expression`        | Numeric value                                        |
| Validation rules          | `validation_metadata.validations[].rule`                                 | Boolean value                                        |
| Summaries                 | `summaries[].total.expression` and `summaries[].components[].expression` | Scalar value                                         |
| Accumulation filters      | `validation_metadata.accumulations[].filter`                             | Boolean match decision                               |

Expression mappings run after top-level XPath mappings are populated.
Expression mappings are evaluated in declaration order, so an expression can
reference XPath fields and earlier expression fields in the same record, but
not later expression fields.

## Grammar

### Values

The parser recognizes:

| Form                 | Example         | Runtime value                                          |
| -------------------- | --------------- | ------------------------------------------------------ |
| Integer              | `42`            | `int64`                                                |
| Float                | `3.14`          | `float64`                                              |
| Boolean              | `true`, `false` | `bool`                                                 |
| Null literal         | `null`          | `nil` in filter null comparisons and evaluator context |
| Double-quoted string | `"ready"`       | Raw string interior                                    |
| Single-quoted string | `'ready'`       | Raw string interior                                    |
| Variable             | `total_amount`  | Value from the current evaluation context              |

Unquoted strings are accepted as filter values in simple filters, for example
`status == active`. In full expressions, unquoted identifiers are variables.

### Recipe Parameters

For scalar `field_mappings[].expression` expressions, Sumpter evaluates the DSL
against a single scope containing extracted record fields, earlier expression
fields, and recipe parameters injected for the run.

Recipe parameters come from `defaults.parameters` plus CLI
`--parameter key=value` overrides. A parameter value is either a **string** or a
**list of strings**. CLI values override manifest defaults before evaluation, and
the same resolved values are emitted as fields in each record (a list as a JSON
array) unless withheld by the selected output format configuration.

Parameters are referenced as **bare DSL variables** (`curated_prefixes`), not
`$curated_prefixes`. There is no `$` parameter syntax.

A **list-of-strings** value is read by the membership/prefix functions
(`starts_with_any`, `value_in`) — see [String Functions](#string-functions). It
lets an operator promote an operationally-volatile set (curated reference data) to
run config instead of inlining it into the recipe:

```yaml
defaults:
  parameters:
    curated_prefixes: ["NM_", "NR_", "NC_"] # list-typed; operator-overridable
```

```bash
# Override at run time with a JSON array — no recipe edit:
sumpter extract files ... --parameter curated_prefixes='["NM_","NR_","NC_","XM_"]'
```

List rules:

- A CLI `--parameter` value becomes a list **only** when it is a valid JSON array
  of strings; otherwise it stays a literal string (so a value that merely contains
  commas is unchanged).
- List members must be non-empty strings. Numbers, booleans, objects, nested or
  mixed arrays, and empty members are rejected at parse time — never coerced.
- An empty list (`[]`) is a valid, explicit empty set: it matches nothing and
  counts as "provided" for `parameters_required` (an empty scalar string does not).

Expression mappings continue to evaluate in declaration order. An expression
can reference:

- XPath fields already extracted for the current record.
- Expression fields declared earlier in `field_mappings`.
- Resolved recipe parameters.

Name collisions are strict failures. If a parameter key matches an XPath
`output_field` or an earlier expression `output_field`, extraction fails rather
than silently preferring either value. Rename the parameter or output field to
make data provenance explicit.

Example:

```yaml
field_mappings:
  - output_field: extracted_tenant
    xpath: tenant
    type: string
  - output_field: tenant_bucket
    expression: 'lower(extracted_tenant) == tenant_label ? "in_scope" : "out_of_scope"'
    type: string
```

Here `tenant_label` can be supplied by `defaults.parameters.tenant_label` or
overridden with `--parameter tenant_label=...`.

This parameter scope applies to scalar field-mapping expressions. It does not
change simple extract filters or the undefined-variable contract: referencing an
undeclared parameter still fails with the existing `undefined variable: <name>`
error.

### Operators

From lowest to highest precedence:

| Precedence | Operators                                | Associativity | Notes                                                      |
| ---------- | ---------------------------------------- | ------------- | ---------------------------------------------------------- | ---- | ---------------------------------------------------------------------------------- |
| 1          | `?:`                                     | Right         | Conditional expression; only the selected branch evaluates |
| 2          | `                                        |               | `, `&&`                                                    | Left | Logical operators; both operands currently evaluate before the operator is applied |
| 3          | `==`, `!=`, `>=`, `<=`, `>`, `<`         | Left          | Comparisons                                                |
| 4          | `+`, `-`                                 | Left          | Numeric addition and subtraction                           |
| 5          | `*`, `/`                                 | Left          | Numeric multiplication and division                        |
| 6          | `!`                                      | Right         | Boolean negation                                           |
| 7          | `(...)`, functions, constants, variables | N/A           | Grouping, function calls, atoms                            |

Parentheses can override grouping:

```text
(a + b) * c
```

### Conditional Expressions

Conditional expressions use C-family ternary syntax:

```text
condition ? then_expression : else_expression
```

The condition must evaluate to a boolean. The result is the selected branch's
value. There is no implicit branch type unification, so fixed-schema outputs
such as Parquet should use branches compatible with the declared field type.

Ternary expressions are right-associative:

```text
a ? b : c ? d : e
```

parses as:

```text
a ? b : (c ? d : e)
```

Only the selected ternary branch is evaluated. This is different from `&&` and
`||`, which currently evaluate both operands before applying the logical
operator.

### Functions

Function names are case-insensitive at evaluation time.

| Function | Signature               | Behavior                                                                                     |
| -------- | ----------------------- | -------------------------------------------------------------------------------------------- |
| `abs`    | `abs(number)`           | Absolute value                                                                               |
| `round`  | `round(number)`         | Round to the nearest integer                                                                 |
| `round`  | `round(number, places)` | Round to the requested non-negative decimal precision; negative precision is clamped to zero |
| `min`    | `min(number, ...)`      | Minimum numeric argument; requires at least one argument                                     |
| `max`    | `max(number, ...)`      | Maximum numeric argument; requires at least one argument                                     |
| `count`  | `count()`               | Returns the `count` variable when present, otherwise `0`                                     |
| `sum`    | `sum()`                 | Returns the `sum` variable when present, otherwise `0.0`                                     |

`count(expr)` and `sum(expr)` are not supported in expression context today.
Use accumulation configuration for data-wide counts and sums.

Function arguments are comma-separated expressions. Commas inside quoted string
literals or nested parentheses do not split arguments.

### String Functions

String function names are case-insensitive, like numeric function names.

| Function          | Signature                                        | Behavior                                                                                                                                                                          |
| ----------------- | ------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `lower`           | `lower(string)`                                  | Unicode-aware lower-casing per Go `strings.ToLower`. Nil-valued argument returns nil.                                                                                             |
| `upper`           | `upper(string)`                                  | Unicode-aware upper-casing per Go `strings.ToUpper`. Nil-valued argument returns nil.                                                                                             |
| `normalize_space` | `normalize_space(string)`                        | Trims leading/trailing Unicode whitespace and collapses internal Unicode whitespace runs to single ASCII spaces. Nil-valued argument returns nil.                                 |
| `mask_tail`       | `mask_tail(string, keep_n)`                      | Replaces all but the last `keep_n` runes with `X`. Nil-valued argument returns nil. Empty string returns empty string. `keep_n >= rune_count(input)` returns the input unchanged. |
| `mask_tail`       | `mask_tail(string, keep_n, mask_char)`           | Same as above, with a custom single-rune mask character.                                                                                                                          |
| `mask_middle`     | `mask_middle(string, head_n, tail_n)`            | Replaces runes between the first `head_n` and last `tail_n` runes with `X`. Nil-valued argument returns nil. `head_n + tail_n >= rune_count(input)` returns the input unchanged.  |
| `mask_middle`     | `mask_middle(string, head_n, tail_n, mask_char)` | Same as above, with a custom single-rune mask character.                                                                                                                          |
| `string_length`   | `string_length(string)`                          | Unicode **rune** count (not byte count). A nil-valued argument is length `0`, so `string_length(x) >= N` is a clean `false` on a missing field rather than a comparison error.    |
| `starts_with_any` | `starts_with_any(string, list)`                  | True when the string value begins with **any** member of the list (a list-typed parameter). Case sensitive. A nil/empty value is false; an empty list is false.                   |
| `value_in`        | `value_in(string, list)`                         | True when the string value **exactly equals** any member of the list. Exact match only — a near miss is false. Case sensitive. A nil value is false; an empty list is false.       |

`starts_with_any` and `value_in` take a **list-of-strings** as their second
argument — an ordinary bare-identifier parameter variable (see
[Recipe Parameters](#recipe-parameters)). They are case sensitive; compose with
`lower(...)` to fold case. A non-list second argument is a loud type error naming
the function and the received type — a scalar and a list are never coerced into
one another. List members must be non-empty: an empty member is rejected both at
parameter parse time and again in the evaluator (an empty prefix would otherwise
match everything). `string_length` lets a length/shape guard move from an
`xpath:` `string-length(...)` helper into an `expression:` mapping, e.g.
`(string_length(accession) >= 5) && starts_with_any(accession, curated_prefixes)`
(use `&&`/`||` for boolean composition — the DSL has no `and`/`or` keywords).

`normalize_space` uses Go `strings.Fields` / `unicode.IsSpace` semantics, so
it treats the full Unicode whitespace class as whitespace. This is broader than
strict XPath 1.0 `normalize-space()`, which recognizes only XML whitespace
(space, tab, carriage return, line feed).

String functions propagate evaluated-as-nil arguments. On current Sumpter this
means the DSL `null` literal or expression results that resolve to nil.
Undefined variables still fail before function dispatch, so `lower(missing)`
returns the existing `undefined variable: missing` error rather than nil.
Numeric functions keep their existing strict numeric contract and error on nil
or non-numeric input.

`mask_tail` and `mask_middle` are rune-aware, not byte-indexed. Masking is
idempotent under the function contract: `mask_tail("XXXXefgh", 4)` returns
`"XXXXefgh"` because all but the last four runes are already `X`.

`mask_tail` and `mask_middle` are visual redaction primitives. They are not
cryptographic, not tokenization, and not hashing. Treat masked output as reduced
fidelity display data, not as a security boundary.

### Reference Table Functions

These resolve a record field against an external **reference table** declared in
the recipe (`defaults.reference_tables`) and loaded once per run. See
[Reference Tables](extract-workflow.md#reference-tables) for the recipe surface.

| Function           | Signature                            | Behavior                                                                                                                                                                |
| ------------------ | ------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `in_reference`     | `in_reference(table, field)`         | Membership (Pattern A). True when `field`'s value is in the distinct set of the table's declared `column`. A nil/empty field is `false` (a miss, never an error).        |
| `lookup_reference` | `lookup_reference(table, field, default)` | Key→value lookup (Pattern B). Returns the table's `value_column` entry for the `key_column` matching `field`; on a miss (or a nil/empty field) returns `default`.   |

The first argument **must be a string literal** — a table name, not a variable or
expression. Record data must never select which run resource is read, so a
non-literal table name is a loud error at config validation (pre-flight), before
any record is processed; an unknown table name (one not declared in the recipe)
fails pre-flight the same way. The match is exact and case sensitive; compose with
`lower(...)` to fold case.

```
# extract.yaml
- output_field: is_curated
  expression: "in_reference('curated', accession)"
  type: boolean
- output_field: molecule_type
  expression: "lookup_reference('molecule', accession, 'unknown')"
  type: string
```

**Exposure note (`lookup_reference`).** Unlike `in_reference`'s boolean,
`lookup_reference` emits a **table value** into the output record. A reference
table's sensitivity therefore flows into output on a Pattern-B lookup — the
standard withhold/output posture applies, so treat enrichment as data exposure on
the same axis as any other emitted field.

Mask counts (`keep_n`, `head_n`, `tail_n`) must be finite, non-negative,
integer-valued numeric arguments and within the platform `int` conversion
range. Fractional values, NaN, infinities, negative values, and values too large
to convert to `int` are rejected with errors naming the function and argument.
Custom `mask_char` values must be single-rune strings.

### Type Rules

Arithmetic operators require numeric operands and return `float64` results.
Division by zero fails.

Comparison behavior is:

- Numeric operands compare numerically.
- `==` and `!=` on non-numeric values use deep equality.
- Ordering comparisons (`>`, `>=`, `<`, `<=`) are only supported for numeric
  operands.
- Logical operators and unary `!` require boolean operands.

Undefined variables fail evaluation with the variable name.
See [String Functions](#string-functions) for the string-function
nil-propagation contract; it does not change undefined-variable behavior.

## Filter Expressions

Sumpter has two filter parsing paths:

- Simple filters use `ParseFilter`, a field/operator/value parser for
  accumulation filters such as `status == active`.
- Advanced filters use the full expression parser when an accumulation filter
  contains `&&`, `||`, `(`, or `)` outside quoted string literals.

### Simple Filter Syntax

```text
field_name == value
field_name != value
field_name > value
field_name >= value
field_name < value
field_name <= value
field_name == null
field_name != null
```

Simple filter values can be booleans, integers, floats, quoted strings,
unquoted strings, or `null`.

Simple filters use this fixed operator order:

```text
==, !=, >=, <=, >, <
```

This order is intentional and load-bearing. Longer or more specific operators
must be considered before their shorter forms so a filter such as
`description >= "value"` splits on `>=`, not `>`. Future filter operators must
preserve explicit list-order semantics.

When a simple filter contains multiple top-level comparison operators, the
first operator in the fixed list wins. For example:

```text
left > mid >= right
```

parses as:

```text
Field: "left > mid"
Operator: ">="
Value: "right"
```

Recipe authors should avoid that shape; it is documented because it is the
current compatibility contract.

## Parser Behavior Contracts

### Quoted String Literals

String literals can use either double quotes (`"..."`) or single quotes
(`'...'`). Operator characters inside quoted string literals are literal
content, not split points, across:

- Binary expression scanning
- Ternary `?` and matching `:` scanning
- Simple filter parsing
- Function argument splitting
- Accumulation filter advanced/simple routing

Examples:

```text
label == "a && b"
description >= "this == that"
status == "what?" ? "yes: ready" : "no"
```

Backslash escapes are honored for quote delimiter detection only. `\"` inside
a double-quoted literal and `\'` inside a single-quoted literal do not end the
literal. Runtime string values keep their raw interior bytes; sequences such
as `\n` or `\"` are not unescaped into different values.

### Unterminated Literals and Bare Quote Characters

Unterminated string literals fail loudly before parser fallback:

```text
name == "Bob
```

Bare unquoted values that contain quote characters also fail because the quote
starts an unterminated string literal:

```text
name == Bob's
```

Quote values containing quote characters explicitly:

```text
name == "Bob's"
```

### Ternary Colon Matching

Ternary parsing uses dedicated matching-`:` logic. The scanner tracks quoted
strings, parenthesis depth, and nested ternary depth so nested branches retain
the documented right-associative behavior.

```text
a ? b ? c : d : e
```

parses as:

```text
a ? (b ? c : d) : e
```

Do not model ternary colon matching as a generic "first colon outside strings"
scan; nested ternaries require pairing depth.

## Forward Compatibility

### Tracked Candidate: Case-When

A richer `case when` form is a tracked v1 candidate if operational use shows
deeply nested ternaries are hard to read. It is not accepted syntax today.

### Demand-Driven Candidate: More Aggregations

`group_by.aggregation` currently supports `sum`. Additional aggregations such
as `avg`, `count`, `min`, or `max` are future demand-driven surface.

### Future Runtime DSL Semantic Versioning

The schema currently accepts `expression_language: sumpter-dsl`. Runtime DSL
semantic version enforcement, including syntax such as
`expression_language: sumpter-dsl@v1.1`, is future work and is not accepted
today.

### Future Lexer or Tokenizer

This reference's grammar and semantic sections are parser-implementation
agnostic. A future tokenizer-based parser must preserve the documented grammar
and semantics. Parser behavior contracts may be updated if tokenizer-level
literal recognition replaces the current scanner edge cases.

## Migration Notes for v0.1.4

### Ternary Expressions

Ternary expressions were added in the v0.1.4 cycle. Existing expressions
continue to work. Authors can use ternaries for concise conditional relabeling:

```yaml
field_mappings:
  - output_field: widget_status_friendly
    expression: 'widget_status == "online" ? "ready" : widget_status'
    type: string
```

### Quoted String Hardening

Quoted string scanning is hardened across parser surfaces. Expressions that
previously relied on bare unquoted values containing quote characters must
quote those values explicitly:

```text
name == "Bob's"
```

Non-DSL v0.1.4 release-note content, such as `min_occurrences`, remains in
`docs/releases/v0.1.4.md` rather than this DSL reference.
