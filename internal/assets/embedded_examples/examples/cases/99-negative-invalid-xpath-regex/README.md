# 99 Negative Invalid XPath Regex

Shows that an invalid regular expression in `matches(SKU, '[unclosed')` is
rejected at config load / preflight before any record extraction runs and
publishes no output files.
