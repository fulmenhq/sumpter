# Record Index Examples

This directory contains example XML record index files demonstrating the `record-index/v0.1.0` schema.

## Files

### `clinvar-sample.recordindex.json`

Example record index for a ClinVar VCV Release XML file (~50GB genomics data with 2.4M variant records).

**Source Characteristics:**

- File: `clinvar_vcv_2024.xml` (~50GB uncompressed)
- Records: 2,400,000 `<VariationArchive>` elements
- Selector: `//VariationArchive`
- Size Range: 2KB - 153KB per record
- Average: ~21KB per record
- Build Time: ~9 minutes

**Use Cases:**

- Large-scale genomics XML processing
- Parallel extraction demonstration
- Integrity verification testing
- Performance benchmarking baseline

### Creating Your Own Index

```bash
# Build index from XML file
sumpter index build input.xml --selector "//RecordElement" --output my-index.recordindex.json

# Verify index integrity
sumpter index verify input.xml --index my-index.recordindex.json

# Use index for parallel extraction
sumpter extract files input.xml --record-index my-index.recordindex.json --workers 8
```

## Schema Documentation

See [schemas/index/v0.1.0/README.md](../../schemas/index/v0.1.0/README.md) for complete schema documentation.

## Related Examples

- [Extract Configs](../config/extract/) - Extraction recipe configurations
- [Sample Data](../data/) - Test XML files
