# 17 XPath String and Regex Contract

Demonstrates the promised XPath string and regular-expression functions
(`lower-case`, `substring-before`, `substring-after`, `substring`,
`string-length`, `normalize-space`, `translate`, `starts-with`, `ends-with`,
`matches`, `replace`) across top-level fields and nested `item_mapping` arrays,
plus the two-step `NaN`-safe numeric coercion guard (`matches()` + `sum()` +
Sumpter DSL `expression` fallback) when an XML numeric element can contain
placeholder text (`<QuotedAmount>TBD</QuotedAmount>`).
