package commands

import (
	"encoding/json"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	docjson "github.com/fulmenhq/sumpter/internal/docnode/json"
)

// inspectPathString is the display form of a path: segments joined with ".",
// with "\" and "." inside a segment escaped by "\". It is lossless; segments
// remain the authoritative identity.
func inspectPathString(segments []string) string {
	var b strings.Builder
	for i, seg := range segments {
		if i > 0 {
			b.WriteByte('.')
		}
		writeInspectSegment(&b, seg)
	}
	return b.String()
}

func writeInspectSegment(b *strings.Builder, seg string) {
	if !strings.ContainsAny(seg, `.\`) {
		b.WriteString(seg)
		return
	}
	for i := 0; i < len(seg); i++ {
		if c := seg[i]; c == '.' || c == '\\' {
			b.WriteByte('\\')
		}
		b.WriteByte(seg[i])
	}
}

// truncateInspectSample applies the sample length cap shared by every input
// format: at most 100 bytes, cut on a rune boundary.
func truncateInspectSample(text string) string {
	if len(text) <= 100 {
		return text
	}
	cut := 97
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "..."
}

// newInspectInput builds the report's input block. Compression is inferred
// from the logical path's extension.
func newInspectInput(fileInfo FileInfo, encoding, format string) InspectInput {
	input := InspectInput{
		Path:             fileInfo.Path,
		SizeBytes:        fileInfo.Size,
		EncodingDetected: encoding,
		Compressed:       false,
		Compression:      "none",
		Format:           format,
	}
	lower := strings.ToLower(fileInfo.Path)
	switch {
	case strings.HasSuffix(lower, ".gz") || strings.HasSuffix(lower, ".gzip"):
		input.Compressed = true
		input.Compression = "gzip"
	case strings.HasSuffix(lower, ".bz2") || strings.HasSuffix(lower, ".bzip2"):
		input.Compressed = true
		input.Compression = "bzip2"
	case strings.HasSuffix(lower, ".xz"):
		input.Compressed = true
		input.Compression = "xz"
	}
	return input
}

// jsonProfiler is the docjson.Handler behind the JSON profile. Every
// StartElement is one element occurrence of the path formed by the open
// elements plus its name.
type jsonProfiler struct {
	maxPaths       int
	samplesPerPath int

	paths map[string]*InspectPath
	// Parallel stacks over the open elements: segment names, display keys,
	// and the tracked path entry (nil when the path cap refused it).
	segments []string
	keys     []string
	open     []*InspectPath

	pathsTruncated   bool
	samplesTruncated bool
}

func newJSONProfiler(opts *InspectOptions) *jsonProfiler {
	return &jsonProfiler{
		maxPaths:       opts.MaxPaths,
		samplesPerPath: opts.SamplesPerPath,
		paths:          make(map[string]*InspectPath),
	}
}

func (p *jsonProfiler) StartElement(name string, kind docjson.Kind) error {
	var b strings.Builder
	if n := len(p.keys); n > 0 {
		b.WriteString(p.keys[n-1])
		b.WriteByte('.')
	}
	writeInspectSegment(&b, name)
	key := b.String()

	p.segments = append(p.segments, name)
	p.keys = append(p.keys, key)

	entry := p.paths[key]
	if entry == nil {
		if len(p.paths) < p.maxPaths {
			entry = &InspectPath{
				Path:       key,
				Segments:   append([]string(nil), p.segments...),
				ValueKinds: &InspectValueKinds{},
			}
			p.paths[key] = entry
		} else {
			p.pathsTruncated = true
		}
	}
	if entry != nil {
		entry.Count++
		entry.ValueKinds.add(kind)
	}
	p.open = append(p.open, entry)
	return nil
}

func (p *jsonProfiler) Text(value string) error {
	entry := p.open[len(p.open)-1]
	if entry == nil {
		return nil
	}
	if len(entry.Samples) < p.samplesPerPath {
		entry.Samples = append(entry.Samples, truncateInspectSample(value))
	} else if p.samplesPerPath > 0 {
		p.samplesTruncated = true
	}
	return nil
}

func (p *jsonProfiler) EndElement() error {
	n := len(p.open) - 1
	p.segments = p.segments[:n]
	p.keys = p.keys[:n]
	p.open = p.open[:n]
	return nil
}

func (k *InspectValueKinds) add(kind docjson.Kind) {
	switch kind {
	case docjson.KindString:
		k.String++
	case docjson.KindNumber:
		k.Number++
	case docjson.KindBool:
		k.Bool++
	case docjson.KindNull:
		k.Null++
	case docjson.KindObject:
		k.Object++
	case docjson.KindArray:
		k.Array++
	}
}

// inspectJSON profiles one JSON document streamed from reader. The walker
// applies every structural guard extraction applies; any walker error fails
// the inspection with the walker's text unchanged.
func inspectJSON(reader io.Reader, fileInfo FileInfo, opts *InspectOptions) (*InspectReportV0, error) {
	prof := newJSONProfiler(opts)
	if err := docjson.Walk(reader, prof); err != nil {
		return nil, err
	}

	paths := make([]InspectPath, 0, len(prof.paths))
	for _, entry := range prof.paths {
		paths = append(paths, *entry)
	}
	// Same ordering as XML: count desc, then path asc.
	sort.Slice(paths, func(i, j int) bool {
		if paths[i].Count == paths[j].Count {
			return paths[i].Path < paths[j].Path
		}
		return paths[i].Count > paths[j].Count
	})

	return &InspectReportV0{
		Version: InspectReportVersion,
		Input:   newInspectInput(fileInfo, "UTF-8", inspectFormatJSON),
		Metrics: InspectMetrics{BytesProcessed: fileInfo.Size},
		Paths:   paths,
		Caps: InspectCaps{
			PathsTruncated:      prof.pathsTruncated,
			AttributesTruncated: false,
			SamplesTruncated:    prof.samplesTruncated,
		},
	}, nil
}

// jsonProfileReport is the wire shape of a JSON-profile report: attributes are
// always present (empty), and the XML-only record analysis sections are
// absent.
type jsonProfileReport struct {
	Version  string            `json:"version"`
	Input    InspectInput      `json:"input"`
	Metrics  InspectMetrics    `json:"metrics"`
	Paths    []jsonProfilePath `json:"paths"`
	Caps     InspectCaps       `json:"caps"`
	Metadata *InspectMetadata  `json:"metadata,omitempty"`
}

type jsonProfilePath struct {
	Path       string             `json:"path"`
	Segments   []string           `json:"segments"`
	Count      int                `json:"count"`
	Attributes []InspectAttribute `json:"attributes"`
	Samples    []string           `json:"samples,omitempty"`
	ValueKinds *InspectValueKinds `json:"value_kinds,omitempty"`
}

// MarshalJSON emits JSON-profile reports in their own shape and every other
// report exactly as the struct tags describe.
func (r InspectReportV0) MarshalJSON() ([]byte, error) {
	if r.Input.Format != inspectFormatJSON {
		type plain InspectReportV0
		return json.Marshal(plain(r))
	}
	out := jsonProfileReport{
		Version:  r.Version,
		Input:    r.Input,
		Metrics:  r.Metrics,
		Paths:    make([]jsonProfilePath, len(r.Paths)),
		Caps:     r.Caps,
		Metadata: r.Metadata,
	}
	for i, p := range r.Paths {
		attrs := p.Attributes
		if attrs == nil {
			attrs = []InspectAttribute{}
		}
		out.Paths[i] = jsonProfilePath{
			Path:       p.Path,
			Segments:   p.Segments,
			Count:      p.Count,
			Attributes: attrs,
			Samples:    p.Samples,
			ValueKinds: p.ValueKinds,
		}
	}
	return json.Marshal(out)
}
