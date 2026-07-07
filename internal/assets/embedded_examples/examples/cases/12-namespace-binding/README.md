# 12 Namespace Binding

Teaches namespace-correct extraction via a `namespaces:` map that binds XPath
prefixes to namespace **URIs** instead of the document's literal prefixes.

The input is a dual-namespace ledger: a default core namespace
(`urn:example:sumpter-records`) plus an extension namespace
(`urn:example:sumpter-records-ext`). The recipe binds the local aliases `n`
(core) and `ext` (extension), then:

- matches the core records with `//n:Record` — resolved by URI, so it selects
  the core `Record` elements even though the document writes them unprefixed
  (default namespace), and it does **not** select the same-local-name
  `ext:Record` elements in the extension namespace;
- reads an extension attribute with `@ext:origin` and an extension element with
  `ext:Annotation`, both bound by URI.

The alias `n` is chosen by the recipe author; it need not match any prefix in
the document. Omitting `namespaces:` preserves the current lenient matching.

Run:

```bash
examples/scripts/run-case.sh examples/cases/12-namespace-binding
```
