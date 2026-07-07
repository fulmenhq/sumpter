package parallel

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"github.com/fulmenhq/sumpter/internal/index"
)

func injectNamespaceContext(xmlData []byte, declarations []index.NamespaceDeclaration) ([]byte, error) {
	declarations = index.NormalizeNamespaceDeclarations(declarations)
	if len(declarations) == 0 {
		return xmlData, nil
	}

	start, err := firstStartElement(xmlData)
	if err != nil {
		return nil, err
	}
	existing := namespacePrefixesOnStart(start)
	missing := make([]index.NamespaceDeclaration, 0, len(declarations))
	for _, decl := range declarations {
		if _, ok := existing[decl.Prefix]; ok {
			continue
		}
		missing = append(missing, decl)
	}
	if len(missing) == 0 {
		return xmlData, nil
	}

	insertAt, err := rootStartTagInsertOffset(xmlData)
	if err != nil {
		return nil, err
	}

	var attrs bytes.Buffer
	for _, decl := range missing {
		attrs.WriteByte(' ')
		if decl.Prefix == "" {
			attrs.WriteString("xmlns")
		} else {
			attrs.WriteString("xmlns:")
			attrs.WriteString(decl.Prefix)
		}
		attrs.WriteString(`="`)
		if err := xml.EscapeText(&attrs, []byte(decl.URI)); err != nil {
			return nil, fmt.Errorf("escape namespace URI: %w", err)
		}
		attrs.WriteByte('"')
	}

	out := make([]byte, 0, len(xmlData)+attrs.Len())
	out = append(out, xmlData[:insertAt]...)
	out = append(out, attrs.Bytes()...)
	out = append(out, xmlData[insertAt:]...)
	return out, nil
}

func firstStartElement(xmlData []byte) (xml.StartElement, error) {
	dec := xml.NewDecoder(bytes.NewReader(xmlData))
	for {
		tok, err := dec.Token()
		if err != nil {
			if err == io.EOF {
				return xml.StartElement{}, fmt.Errorf("record fragment has no root element")
			}
			return xml.StartElement{}, fmt.Errorf("parse record root start: %w", err)
		}
		if start, ok := tok.(xml.StartElement); ok {
			return start, nil
		}
	}
}

func namespacePrefixesOnStart(start xml.StartElement) map[string]struct{} {
	prefixes := map[string]struct{}{}
	for _, attr := range start.Attr {
		switch {
		case attr.Name.Space == "" && attr.Name.Local == "xmlns":
			prefixes[""] = struct{}{}
		case attr.Name.Space == "xmlns":
			prefixes[attr.Name.Local] = struct{}{}
		}
	}
	return prefixes
}

func rootStartTagInsertOffset(xmlData []byte) (int, error) {
	inQuote := byte(0)
	for i, b := range xmlData {
		if inQuote != 0 {
			if b == inQuote {
				inQuote = 0
			}
			continue
		}
		switch b {
		case '\'', '"':
			inQuote = b
		case '>':
			insertAt := i
			j := i - 1
			for j >= 0 && isXMLSpace(xmlData[j]) {
				j--
			}
			if j >= 0 && xmlData[j] == '/' {
				insertAt = j
			}
			return insertAt, nil
		}
	}
	return 0, fmt.Errorf("record fragment root start tag is not closed")
}

func isXMLSpace(b byte) bool {
	return strings.ContainsRune(" \t\r\n", rune(b))
}
