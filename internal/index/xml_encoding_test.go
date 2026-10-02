package index

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuilderASCIIOriginalByteRanges(t *testing.T) {
	for _, label := range []string{"ASCII", "ascii", "US-ASCII", "UTF-8"} {
		t.Run(label, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "records.xml")
			fragments := []string{`<p:Record id="one"><Text>&#xE9; &amp;</Text></p:Record>`, `<p:Record id="two"/>`}
			source := fmt.Sprintf("<?xml version=\"1.0\" encoding=\"%s\"?>\r\n<Root xmlns:p=\"urn:example:records\">\r\n%s\r\n%s\r\n</Root>", label, fragments[0], fragments[1])
			if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}
			idx, err := NewBuilder(BuildOptions{InputPath: path, Selector: "//Record"}).Build()
			if err != nil {
				t.Fatal(err)
			}
			if idx.Source.OffsetKind != OffsetKindSourceBytes || idx.Source.SizeBytes != int64(len(source)) {
				t.Fatalf("source = %+v", idx.Source)
			}
			hash := sha256.Sum256([]byte(source))
			if idx.Source.SHA256 != hex.EncodeToString(hash[:]) {
				t.Fatal("source hash changed")
			}
			if len(idx.Records) != len(fragments) {
				t.Fatalf("records = %d", len(idx.Records))
			}
			for i, record := range idx.Records {
				start := strings.Index(source, fragments[i])
				if record.RecordNum != i+1 || record.StartOffset != int64(start) || record.EndOffset != int64(start+len(fragments[i])) || record.SizeBytes != int64(len(fragments[i])) {
					t.Fatalf("record %d = %+v", i, record)
				}
				hash := sha256.Sum256([]byte(source[record.StartOffset:record.EndOffset]))
				if record.SHA256 != hex.EncodeToString(hash[:]) {
					t.Fatalf("record %d original-byte hash differs", i)
				}
			}
		})
	}
}

func TestBuilderASCIIInvalidBytes(t *testing.T) {
	for _, body := range []string{`<Root><Record>` + "\xff" + `</Record></Root>`, `<Root><Record>café</Record></Root>`, `<Root><Record/><!--é--></Root>`} {
		t.Run(fmt.Sprintf("%x", body), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid.xml")
			if err := os.WriteFile(path, []byte(`<?xml version="1.0" encoding="ASCII"?>`+body), 0o600); err != nil {
				t.Fatal(err)
			}
			idx, err := NewBuilder(BuildOptions{InputPath: path, Selector: "//Record"}).Build()
			if idx != nil || err == nil || !strings.Contains(err.Error(), "non-ASCII byte") {
				t.Fatalf("index=%v err=%v", idx, err)
			}
		})
	}
}

func TestBuilderASCIIUTF8MultibyteControl(t *testing.T) {
	path := filepath.Join(t.TempDir(), "utf8.xml")
	fragment := `<Record>café 日本語</Record>`
	source := `<?xml version="1.0" encoding="UTF-8"?><Root>` + fragment + `</Root>`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	idx, err := NewBuilder(BuildOptions{InputPath: path, Selector: "//Record"}).Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Records) != 1 {
		t.Fatal("missing UTF-8 record")
	}
	record := idx.Records[0]
	if source[record.StartOffset:record.EndOffset] != fragment {
		t.Fatal("UTF-8 range changed")
	}
	hash := sha256.Sum256([]byte(fragment))
	if record.SHA256 != hex.EncodeToString(hash[:]) {
		t.Fatal("UTF-8 hash changed")
	}
}
