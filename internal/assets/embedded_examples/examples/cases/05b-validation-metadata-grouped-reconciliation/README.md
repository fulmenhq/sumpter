# 05b Validation Metadata Grouped Reconciliation

Shows the same order-total reconciliation pattern as case 05, but uses
`group_by` to generate one reconciliation component per observed category.

Note: `_validation.record_count` currently reflects records counted by
accumulations, not records traversed by `group_by`; a grouped-only
reconciliation can therefore show `record_count: 0` while still emitting
grouped components.
