package extract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/provenance"
)

func extractDataBytes(t *testing.T, res ExtractResult) []byte {
	t.Helper()
	var blocks []interface{}
	for _, rec := range res.Records {
		blocks = append(blocks, rec["extract"])
	}
	b, err := json.Marshal(blocks)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestJSONByteOrderMarkExtractsIdentically(t *testing.T) {
	const doc = `{"D":{"Order":[{"id":"1"},{"id":"2"}]}}`
	plainPath := writeTempFile(t, "plain.json", doc)
	bomPath := writeTempFile(t, "bom.json", "\xef\xbb\xbf"+doc)

	sig, ext := jsonProcessConfigs()
	plain := ProcessFile(plainPath, sig, ext, nil, false)
	sig, ext = jsonProcessConfigs()
	bom := ProcessFile(bomPath, sig, ext, nil, false)
	if plain.Error != nil || bom.Error != nil {
		t.Fatalf("errors: plain=%v bom=%v", plain.Error, bom.Error)
	}
	if len(bom.Records) != 2 {
		t.Fatalf("BOM input produced %d records, want 2", len(bom.Records))
	}
	if string(extractDataBytes(t, plain)) != string(extractDataBytes(t, bom)) {
		t.Fatal("BOM and no-BOM inputs produced different extract.data")
	}

	// The input digest covers the raw file bytes, BOM included.
	raw, err := os.ReadFile(bomPath)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(raw)
	got, _, err := provenance.HashLocalInput(bomPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != "sha256:"+hex.EncodeToString(want[:]) {
		t.Fatalf("input digest %s is not over the raw bytes", got)
	}
	plainDigest, _, _ := provenance.HashLocalInput(plainPath)
	if plainDigest == got {
		t.Fatal("BOM and no-BOM inputs share a digest; the BOM must be part of the hashed bytes")
	}
}

func TestJSONEncodingFaultsEmitZeroRecords(t *testing.T) {
	pad := strings.Repeat(" ", 1<<20)
	far := strings.Repeat("x", 1<<20)
	longKey := strings.Repeat("k", 70<<10)
	for name, src := range map[string]string{
		"UTF-16LE BOM":                   "\xff\xfe{\x00}\x00",
		"value then invalid byte":        "{\"D\":{\"Order\":[{\"id\":\"1\"}]}}\xff",
		"value, spaces, invalid byte":    "{\"D\":{\"Order\":[{\"id\":\"1\"}]}}" + pad + "\xff",
		"syntax fault before invalid":    "{\"D\":{\"Order\":[{\"id\":tru}]}, \"x\":\"\xff\"}",
		"invalid before syntax fault":    "{\"D\":{\"Order\":[{\"id\":\"\xff\"}]}, \"x\":tru}",
		"truncated rune at end of input": "{\"D\":{\"Order\":[{\"id\":\"1\"}]}}" + pad + "\xe4\xb8",
		"late string is the only fault":  "{\"D\":{\"Order\":[{\"id\":\"1\"},{\"id\":\"" + far + "\xff\"}]}}",
		"syntax fault, invalid 1 MiB on": "{\"D\":{\"Order\":[{\"id\":\"1\"},{\"id\":tru}]}, \"x\":\"" + far + "\xff\"}",
		"invalid, syntax fault 1 MiB on": "{\"D\":{\"Order\":[{\"id\":\"1\"},{\"id\":\"\xff" + far + "\"}]}, \"x\":tru}",
		"long duplicate key":             "{\"D\":{\"Order\":[{\"id\":\"1\"}]},\"" + longKey + "\":1,\"" + longKey + "\":2}",
	} {
		t.Run(name, func(t *testing.T) {
			sig, ext := jsonProcessConfigs()
			res := ProcessFile(writeTempFile(t, "bad.json", src), sig, ext, nil, false)
			if res.Error == nil {
				t.Fatal("expected an error")
			}
			if len(res.Records) != 0 {
				t.Fatalf("emitted %d records", len(res.Records))
			}
		})
	}
}
