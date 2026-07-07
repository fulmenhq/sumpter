package store

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/fulmenhq/sumpter/internal/index"
)

func TestEncodeBinaryRecord(t *testing.T) {
	rec := &index.RecordMetadata{
		RecordNum:           42,
		StartOffset:         1000,
		EndOffset:           2000,
		SizeBytes:           1000,
		Depth:               3,
		NamespaceContextRef: 7,
		SHA256:              "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", // SHA256 of empty string
	}

	buf := make([]byte, BinaryRecordWidth)
	err := EncodeBinaryRecord(buf, rec)
	if err != nil {
		t.Fatalf("EncodeBinaryRecord failed: %v", err)
	}

	// Decode and verify roundtrip
	decoded, err := DecodeBinaryRecord(buf)
	if err != nil {
		t.Fatalf("DecodeBinaryRecord failed: %v", err)
	}

	if decoded.RecordNum != rec.RecordNum {
		t.Errorf("RecordNum mismatch: got %d, want %d", decoded.RecordNum, rec.RecordNum)
	}
	if decoded.StartOffset != rec.StartOffset {
		t.Errorf("StartOffset mismatch: got %d, want %d", decoded.StartOffset, rec.StartOffset)
	}
	if decoded.EndOffset != rec.EndOffset {
		t.Errorf("EndOffset mismatch: got %d, want %d", decoded.EndOffset, rec.EndOffset)
	}
	if decoded.SizeBytes != rec.SizeBytes {
		t.Errorf("SizeBytes mismatch: got %d, want %d", decoded.SizeBytes, rec.SizeBytes)
	}
	if decoded.Depth != rec.Depth {
		t.Errorf("Depth mismatch: got %d, want %d", decoded.Depth, rec.Depth)
	}
	if decoded.SHA256 != rec.SHA256 {
		t.Errorf("SHA256 mismatch: got %s, want %s", decoded.SHA256, rec.SHA256)
	}
	if decoded.NamespaceContextRef != rec.NamespaceContextRef {
		t.Errorf("NamespaceContextRef mismatch: got %d, want %d", decoded.NamespaceContextRef, rec.NamespaceContextRef)
	}
}

func TestEncodeBinaryRecord_LargeValues(t *testing.T) {
	rec := &index.RecordMetadata{
		RecordNum:   1000000,
		StartOffset: 1 << 40,               // 1TB offset
		EndOffset:   (1 << 40) + (1 << 30), // 1TB + 1GB
		SizeBytes:   1 << 30,               // 1GB
		Depth:       100,
		SHA256:      "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
	}

	buf := make([]byte, BinaryRecordWidth)
	err := EncodeBinaryRecord(buf, rec)
	if err != nil {
		t.Fatalf("EncodeBinaryRecord failed: %v", err)
	}

	decoded, err := DecodeBinaryRecord(buf)
	if err != nil {
		t.Fatalf("DecodeBinaryRecord failed: %v", err)
	}

	if decoded.StartOffset != rec.StartOffset {
		t.Errorf("StartOffset mismatch: got %d, want %d", decoded.StartOffset, rec.StartOffset)
	}
	if decoded.EndOffset != rec.EndOffset {
		t.Errorf("EndOffset mismatch: got %d, want %d", decoded.EndOffset, rec.EndOffset)
	}
}

func TestEncodeBinaryRecord_BufferTooSmall(t *testing.T) {
	rec := &index.RecordMetadata{
		SHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	}

	buf := make([]byte, BinaryRecordWidth-1) // Too small
	err := EncodeBinaryRecord(buf, rec)
	if err == nil {
		t.Fatal("Expected error for small buffer, got nil")
	}
}

func TestDecodeBinaryRecord_BufferTooSmall(t *testing.T) {
	buf := make([]byte, legacyBinaryRecordWidth-1) // Too small
	_, err := DecodeBinaryRecord(buf)
	if err == nil {
		t.Fatal("Expected error for small buffer, got nil")
	}
}

func TestDecodeBinaryRecord_LegacyWidth(t *testing.T) {
	rec := &index.RecordMetadata{
		RecordNum:   42,
		StartOffset: 1000,
		EndOffset:   2000,
		SizeBytes:   1000,
		Depth:       3,
		SHA256:      "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	}

	buf := make([]byte, BinaryRecordWidth)
	if err := EncodeBinaryRecord(buf, rec); err != nil {
		t.Fatalf("EncodeBinaryRecord failed: %v", err)
	}
	decoded, err := DecodeBinaryRecord(buf[:legacyBinaryRecordWidth])
	if err != nil {
		t.Fatalf("DecodeBinaryRecord legacy width failed: %v", err)
	}
	if decoded.NamespaceContextRef != 0 {
		t.Errorf("legacy rows should default NamespaceContextRef to 0, got %d", decoded.NamespaceContextRef)
	}
}

func TestEncodeBinaryRecord_InvalidSHA256(t *testing.T) {
	rec := &index.RecordMetadata{
		SHA256: "tooshort",
	}

	buf := make([]byte, BinaryRecordWidth)
	err := EncodeBinaryRecord(buf, rec)
	if err == nil {
		t.Fatal("Expected error for invalid SHA256, got nil")
	}
}

func TestEncodeBinaryRecord_RejectsOutOfRangeFields(t *testing.T) {
	validSHA := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	tests := []struct {
		name string
		rec  *index.RecordMetadata
	}{
		{
			name: "nil record",
			rec:  nil,
		},
		{
			name: "negative start offset",
			rec:  &index.RecordMetadata{StartOffset: -1, SHA256: validSHA},
		},
		{
			name: "negative end offset",
			rec:  &index.RecordMetadata{EndOffset: -1, SHA256: validSHA},
		},
		{
			name: "negative size",
			rec:  &index.RecordMetadata{SizeBytes: -1, SHA256: validSHA},
		},
		{
			name: "negative depth",
			rec:  &index.RecordMetadata{Depth: -1, SHA256: validSHA},
		},
		{
			name: "negative record number",
			rec:  &index.RecordMetadata{RecordNum: -1, SHA256: validSHA},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			buf := make([]byte, BinaryRecordWidth)
			if err := EncodeBinaryRecord(buf, tc.rec); err == nil {
				t.Fatal("expected range error, got nil")
			}
		})
	}
}

func TestDecodeBinaryRecord_RejectsOutOfRangeInt64(t *testing.T) {
	buf := make([]byte, BinaryRecordWidth)
	binary.LittleEndian.PutUint64(buf[0:8], maxInt64AsUint64+1)

	if _, err := DecodeBinaryRecord(buf); err == nil {
		t.Fatal("expected int64 range error, got nil")
	}
}

func TestHexConversion(t *testing.T) {
	testCases := []struct {
		name    string
		hex     string
		wantErr bool
	}{
		{
			name:    "valid lowercase",
			hex:     "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			wantErr: false,
		},
		{
			name:    "valid uppercase",
			hex:     "E3B0C44298FC1C149AFBF4C8996FB92427AE41E4649B934CA495991B7852B855",
			wantErr: false,
		},
		{
			name:    "valid mixed case",
			hex:     "E3b0C44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852B855",
			wantErr: false,
		},
		{
			name:    "too short",
			hex:     "e3b0c44298fc1c14",
			wantErr: true,
		},
		{
			name:    "too long",
			hex:     "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b85500",
			wantErr: true,
		},
		{
			name:    "invalid char",
			hex:     "g3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := hexToBytes32(tc.hex)
			if tc.wantErr && err == nil {
				t.Error("Expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
		})
	}
}

func TestDeriveSeekablePaths(t *testing.T) {
	testCases := []struct {
		input       string
		wantHeader  string
		wantRecords string
	}{
		{
			input:       "/path/to/file.recordindex.json",
			wantHeader:  "/path/to/file.recordindex.header.json",
			wantRecords: "/path/to/file.recordindex.records.szst",
		},
		{
			input:       "/path/to/file.recordindex.header.json",
			wantHeader:  "/path/to/file.recordindex.header.json",
			wantRecords: "/path/to/file.recordindex.records.szst",
		},
		{
			input:       "/path/to/file",
			wantHeader:  "/path/to/file.recordindex.header.json",
			wantRecords: "/path/to/file.recordindex.records.szst",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			header, records := DeriveSeekablePaths(tc.input)
			if header != tc.wantHeader {
				t.Errorf("Header mismatch: got %s, want %s", header, tc.wantHeader)
			}
			if records != tc.wantRecords {
				t.Errorf("Records mismatch: got %s, want %s", records, tc.wantRecords)
			}
		})
	}
}

func TestBinaryRecordWidth(t *testing.T) {
	// Verify the constant matches our expected layout:
	// 8 (start) + 8 (end) + 8 (size) + 4 (depth) + 4 (recordnum)
	// + 32 (sha256) + 4 (namespace_context_ref) = 68.
	expected := 8 + 8 + 8 + 4 + 4 + 32 + 4
	if BinaryRecordWidth != expected {
		t.Errorf("BinaryRecordWidth mismatch: got %d, want %d", BinaryRecordWidth, expected)
	}
}

func TestPublishSeekablePairRestoresExistingPairWhenRecordsPublishFails(t *testing.T) {
	tmpDir := t.TempDir()
	headerFinal := filepath.Join(tmpDir, "sample.recordindex.header.json")
	recordsFinal := filepath.Join(tmpDir, "sample.recordindex.records.szst")
	headerTmp := filepath.Join(tmpDir, "sample.recordindex.header.json.tmp")
	missingRecordsTmp := filepath.Join(tmpDir, "missing.recordindex.records.szst.tmp")

	oldHeader := []byte(`{"version":"old"}`)
	oldRecords := []byte("old-records")
	newHeader := []byte(`{"version":"new"}`)

	if err := os.WriteFile(headerFinal, oldHeader, 0o600); err != nil {
		t.Fatalf("write old header: %v", err)
	}
	if err := os.WriteFile(recordsFinal, oldRecords, 0o600); err != nil {
		t.Fatalf("write old records: %v", err)
	}
	if err := os.WriteFile(headerTmp, newHeader, 0o600); err != nil {
		t.Fatalf("write temp header: %v", err)
	}

	if _, _, err := publishSeekablePair(missingRecordsTmp, recordsFinal, headerTmp, headerFinal); err == nil {
		t.Fatal("publishSeekablePair succeeded with missing records temp, want error")
	}

	assertFileContent(t, headerFinal, oldHeader)
	assertFileContent(t, recordsFinal, oldRecords)
	if matches, err := filepath.Glob(filepath.Join(tmpDir, "*.bak-*")); err != nil {
		t.Fatalf("glob backups: %v", err)
	} else if len(matches) != 0 {
		t.Fatalf("backup artifacts were not restored/removed: %v", matches)
	}
}

func assertFileContent(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != string(want) {
		t.Fatalf("%s content = %q, want %q", path, string(got), string(want))
	}
}
