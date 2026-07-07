# Namespace conformance fixtures

Synthetic, hermetic fixtures for XML namespace-correct extraction. They are the
acceptance oracle for the namespace-binding feature (URI-resolved XPath) and the
standing mode-parity regression gate (whole-document / streaming / indexed
produce identical records).

## Authored from scratch — never reduced from real data

Every fixture here is **authored from the generic pattern below**, not minimized
from any real or downstream sample. Fixture XML **and** any expected-output JSON
are equal leak surfaces: residual real values, element/attribute names, or
structure can survive a "minimization". Generic vocabulary only —
`urn:example:sumpter-records`, `Record`, `Label`, `Amount`, `PostedDate`. No
client, vertical, or trade-format names. New fixtures added here must follow the
same rule and pass the standing neutralization scan.

## The core vocabulary

One tiny synthetic ledger vocabulary with two namespace URIs:

- core: `urn:example:sumpter-records`
- extension: `urn:example:sumpter-records-ext`

A core record carries: an unprefixed `id` attribute (in no namespace, so it
reads identically across every shape), and core-namespace `Label`, `Amount`,
`PostedDate` child elements.

## The trio — identical core content, three serializations

| File | Shape | Namespaces |
|---|---|---|
| `prefixed.xml` | fully prefixed (`<n:Record>`, `xmlns:n=…`) | core only |
| `default-ns.xml` | default namespace (`<Record>`, `xmlns=…`) | core only |
| `dual.xml` | default core ns + extension ns | core + ext |

The three files hold **identical core logical content** (records `R-0001`,
`R-0002`), so "a URI-bound recipe extracts identical core records from all
three" is a meaningful invariant. `dual.xml` adds extension-only material for
separate assertions — do not fold extension fields into the core-only shapes'
expectations.

Invariants the trio proves:

- **URI binding is serialization-stable:** `//n:Record` bound to the core URI
  matches the same two records whether the document wrote them prefixed,
  default-namespaced, or dual.
- **Same-local-name-in-two-URIs disambiguation** (`dual.xml`): a `Record` exists
  in *both* the core URI and the extension URI. A core-bound `//n:Record`
  selects the two core records; an ext-bound `//ext:Record` selects the two
  extension records. Literal-prefix or bare-name matching cannot make this
  distinction.
- **Extension attribute binding** (`dual.xml`): `@ext:origin` resolves by URI.

## Adversarial fixtures (`adversarial/`)

Negative-test oracles for the mode-parity work (indexed re-injection + scoping
fidelity):

- `entity-escaped-uri.xml` — a namespace URI whose value carries a
  markup-breakout sequence (`xmlns:v="a&quot;/&gt;&lt;x&gt;"`). Proves that when
  a captured `xmlns` value is re-injected into an indexed slice it is escaped /
  structurally set, never naively concatenated.
- `prefix-shadowing.xml` — the prefix `p` is bound to the core URI at the root
  and **rebound to the extension URI** on an inner element. Proves that in-scope
  namespace capture is faithful: a core-URI-bound recipe matches the outer
  `p:Label` (core) but not the inner one (extension), despite the identical
  literal prefix.

## Large / streaming variant — generated, not committed

`gen/` is a deterministic generator (no clock, no randomness) that inflates the
core content to cross the 100 MB streaming threshold on demand, so the streaming
path is exercised without a large committed blob:

```sh
# exact count
go run ./tests/fixtures/namespace-conformance/gen -shape b -records 500000 -out big.xml
# or size-targeted
go run ./tests/fixtures/namespace-conformance/gen -shape b -target-mb 120 -out big.xml
```

`-shape` selects `a` (prefixed), `b` (default-ns), or `c` (dual). The generated
core records share the trio's vocabulary and field shape, so a bound recipe
extracts the same record shape at any size.

## Byte-exact — do not reflow

These live under `tests/fixtures/` (excluded from `make fmt-json`). Any
expected-output JSON added alongside them is a byte-exact contract fixture: do
not let `make fmt-docs` reflow it (`git checkout -- tests/fixtures/` if it does).
