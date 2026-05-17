package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type stableRecord struct {
	Validation interface{} `json:"_validation,omitempty"`
	Extract    interface{} `json:"extract"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: canonicalize <jsonl-file>")
		os.Exit(2)
	}

	file, err := openInput(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "open: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = file.Close() }()

	var records []stableRecord
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var raw map[string]interface{}
		if err := json.Unmarshal(scanner.Bytes(), &raw); err != nil {
			fmt.Fprintf(os.Stderr, "decode: %v\n", err)
			os.Exit(1)
		}
		record := stableRecord{Extract: raw["extract"]}
		if validation, ok := raw["_validation"]; ok {
			record.Validation = sanitizeValidation(validation)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "scan: %v\n", err)
		os.Exit(1)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(map[string]interface{}{"records": records}); err != nil {
		fmt.Fprintf(os.Stderr, "encode: %v\n", err)
		os.Exit(1)
	}
}

func openInput(path string) (*os.File, error) {
	cleanPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(cleanPath)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s is a directory", cleanPath)
	}
	return os.Open(cleanPath) // #nosec G703,G304 -- local test helper reads the harness-generated JSONL file.
}

func sanitizeValidation(value interface{}) interface{} {
	m, ok := value.(map[string]interface{})
	if !ok {
		return value
	}
	delete(m, "extraction_timestamp")
	return m
}
