package index

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// IndexWriter receives record metadata as records are discovered and publishes
// an index artifact after summary statistics are known. Prepare must complete
// all fallible artifact generation without changing final paths. Commit may
// publish final paths while retaining enough state for Close to roll back until
// Complete is called.
type IndexWriter interface {
	Start(index *RecordIndex) error
	AppendRecord(record RecordMetadata) error
	Prepare(index *RecordIndex) error
	Commit() error
	Complete() error
	Close() error
}

// JSONIndexWriter writes a JSON record index progressively. It writes the
// records array before summary and metadata so records do not need to be kept
// in memory until the build completes.
type JSONIndexWriter struct {
	path       string
	tmpPath    string
	backupPath string
	file       *os.File
	started    bool
	prepared   bool
	committed  bool
	completed  bool
	finalized  bool
	closed     bool
	recordSeen bool
}

// NewJSONIndexWriter returns a progressive JSON index writer.
func NewJSONIndexWriter(path string) *JSONIndexWriter {
	return &JSONIndexWriter{path: path}
}

func (w *JSONIndexWriter) Start(index *RecordIndex) (err error) {
	if index == nil {
		return fmt.Errorf("record index header is required")
	}
	if w.started {
		return fmt.Errorf("JSON index writer already started")
	}

	dir := filepath.Dir(w.path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	file, err := os.CreateTemp(dir, filepath.Base(w.path)+".tmp-*") // #nosec G304 - output directory is user-provided CLI argument
	if err != nil {
		return fmt.Errorf("failed to create temporary output file: %w", err)
	}
	w.file = file
	w.tmpPath = file.Name()
	w.started = true
	defer func() {
		if err != nil {
			_ = w.Close()
		}
	}()

	header := *index
	header.Version = SchemaVersion
	header.Records = nil
	NormalizeRecordIndex(&header)

	if _, err := fmt.Fprintln(w.file, "{"); err != nil {
		return err
	}
	if err := writeJSONField(w.file, "version", header.Version, true); err != nil {
		return err
	}
	if err := writeJSONField(w.file, "source", header.Source, true); err != nil {
		return err
	}
	if err := writeJSONField(w.file, "selector", header.Selector, true); err != nil {
		return err
	}
	if err := writeJSONField(w.file, "namespace_contexts", header.NamespaceContexts, true); err != nil {
		return err
	}
	if _, err := fmt.Fprint(w.file, "  \"records\": ["); err != nil {
		return err
	}

	return nil
}

func (w *JSONIndexWriter) AppendRecord(record RecordMetadata) error {
	if !w.started {
		return fmt.Errorf("JSON index writer not started")
	}
	if w.finalized {
		return fmt.Errorf("JSON index writer already finalized")
	}

	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("failed to encode record %d: %w", record.RecordNum, err)
	}

	if w.recordSeen {
		if _, err := fmt.Fprint(w.file, ","); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w.file, "\n    %s", data); err != nil {
		return err
	}
	w.recordSeen = true
	return nil
}

func (w *JSONIndexWriter) Prepare(index *RecordIndex) error {
	if index == nil {
		return fmt.Errorf("record index summary is required")
	}
	if !w.started {
		return fmt.Errorf("JSON index writer not started")
	}
	if w.prepared {
		return nil
	}

	if w.recordSeen {
		if _, err := fmt.Fprint(w.file, "\n  ],\n"); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprint(w.file, "],\n"); err != nil {
			return err
		}
	}

	final := *index
	final.Version = SchemaVersion
	final.Records = nil
	NormalizeRecordIndex(&final)

	if err := writeJSONField(w.file, "summary", final.Summary, true); err != nil {
		return err
	}
	if err := writeJSONField(w.file, "metadata", final.Metadata, false); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w.file, "\n}"); err != nil {
		return err
	}
	if err := w.file.Close(); err != nil {
		w.file = nil
		return fmt.Errorf("failed to close temporary JSON index: %w", err)
	}
	w.file = nil

	w.prepared = true
	return nil
}

func (w *JSONIndexWriter) Commit() error {
	if !w.prepared {
		return fmt.Errorf("JSON index writer not prepared")
	}
	if w.committed {
		return nil
	}

	backupPath, backedUp, err := stageExistingPath(w.path)
	if err != nil {
		return fmt.Errorf("failed to stage existing JSON index for replacement: %w", err)
	}
	if backedUp {
		w.backupPath = backupPath
	}
	if err := os.Rename(w.tmpPath, w.path); err != nil {
		restoreErr := restoreStagedPath(w.backupPath, w.path)
		return errors.Join(fmt.Errorf("failed to publish JSON index: %w", err), restoreErr)
	}
	w.tmpPath = ""
	w.committed = true
	return nil
}

func (w *JSONIndexWriter) Complete() error {
	if !w.committed && w.prepared {
		return fmt.Errorf("JSON index writer not committed")
	}
	if w.completed {
		return nil
	}
	w.completed = true
	w.finalized = true
	if w.backupPath != "" {
		if err := os.Remove(w.backupPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove JSON index backup: %w", err)
		}
		w.backupPath = ""
	}
	return nil
}

func (w *JSONIndexWriter) Finalize(index *RecordIndex) error {
	if err := w.Prepare(index); err != nil {
		return err
	}
	if err := w.Commit(); err != nil {
		return err
	}
	return w.Complete()
}

func (w *JSONIndexWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	var err error
	if w.file != nil {
		err = w.file.Close()
		w.file = nil
	}
	if !w.completed {
		if w.committed {
			err = errors.Join(err, os.Remove(w.path), restoreStagedPath(w.backupPath, w.path))
			w.backupPath = ""
			w.committed = false
		} else if w.backupPath != "" {
			err = errors.Join(err, restoreStagedPath(w.backupPath, w.path))
			w.backupPath = ""
		}
		if w.tmpPath != "" {
			err = errors.Join(err, os.Remove(w.tmpPath))
			w.tmpPath = ""
		}
	} else if w.tmpPath != "" {
		removeErr := os.Remove(w.tmpPath)
		w.tmpPath = ""
		return errors.Join(err, removeErr)
	}
	return err
}

type collectingIndexWriter struct {
	records []RecordMetadata
}

func (w *collectingIndexWriter) Start(_ *RecordIndex) error {
	w.records = make([]RecordMetadata, 0, 1024)
	return nil
}

func (w *collectingIndexWriter) AppendRecord(record RecordMetadata) error {
	w.records = append(w.records, record)
	return nil
}

func (w *collectingIndexWriter) Finalize(_ *RecordIndex) error {
	return nil
}

func (w *collectingIndexWriter) Prepare(index *RecordIndex) error {
	return w.Finalize(index)
}

func (w *collectingIndexWriter) Commit() error {
	return nil
}

func (w *collectingIndexWriter) Complete() error {
	return nil
}

func (w *collectingIndexWriter) Close() error {
	return nil
}

func stageExistingPath(path string) (string, bool, error) {
	backupPath := fmt.Sprintf("%s.bak-%d-%d", path, os.Getpid(), time.Now().UnixNano())
	err := os.Rename(path, backupPath)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return backupPath, true, nil
}

func restoreStagedPath(backupPath, finalPath string) error {
	if backupPath == "" {
		return nil
	}
	if err := os.Rename(backupPath, finalPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func writeJSONField(file *os.File, name string, value interface{}, trailingComma bool) error {
	data, err := json.MarshalIndent(value, "  ", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode %s: %w", name, err)
	}
	comma := ""
	if trailingComma {
		comma = ","
	}
	_, err = fmt.Fprintf(file, "  %q: %s%s\n", name, data, comma)
	return err
}
