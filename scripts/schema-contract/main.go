// Command schema-contract checks the schema bundle and maintains its catalog.
//
//	go run ./scripts/schema-contract check [dir]    # ids, manifests, reference audit, catalog drift
//	go run ./scripts/schema-contract catalog [dir]  # regenerate <dir>/index.json
//
// dir defaults to "schemas".
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fulmenhq/sumpter/internal/schemacontract"
)

func main() {
	if len(os.Args) < 2 {
		fail(errors.New("usage: schema-contract check|catalog [dir]"))
	}
	dir := "schemas"
	if len(os.Args) > 2 {
		dir = os.Args[2]
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		fail(err)
	}
	defer func() { _ = root.Close() }()
	switch os.Args[1] {
	case "check":
		err = check(root)
	case "catalog":
		err = writeCatalog(root)
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}

// build loads and fully checks the bundle, then builds its catalog. All file
// access goes through root, so nothing outside the bundle directory is read.
func build(root *os.Root) ([]byte, int, error) {
	fsys := root.FS()
	b, err := schemacontract.LoadBundle(fsys)
	if err != nil {
		return nil, 0, err
	}
	manifests, err := schemacontract.LoadManifests(fsys)
	if err != nil {
		return nil, 0, err
	}
	errs := schemacontract.CheckIDs(b)
	errs = append(errs, schemacontract.CheckManifests(b, manifests)...)
	errs = append(errs, schemacontract.Audit(b, schemacontract.CompanionsFrom(b, manifests))...)
	if len(errs) > 0 {
		return nil, 0, errors.Join(errs...)
	}
	catalog, err := schemacontract.BuildCatalog(fsys, b, manifests)
	if err != nil {
		return nil, 0, err
	}
	data, err := catalog.Encode()
	return data, len(b.Resources), err
}

func check(root *os.Root) error {
	want, n, err := build(root)
	if err != nil {
		return err
	}
	have, err := root.ReadFile(schemacontract.CatalogFile)
	if err != nil {
		return fmt.Errorf("read catalog: %w (run: make schema-catalog)", err)
	}
	if !bytes.Equal(have, want) {
		return fmt.Errorf("%s is out of date with the bundle (run: make schema-catalog)", filepath.Join(root.Name(), schemacontract.CatalogFile))
	}
	fmt.Printf("schema bundle ok: %d resources, catalog current\n", n)
	return nil
}

func writeCatalog(root *os.Root) error {
	data, _, err := build(root)
	if err != nil {
		return err
	}
	if err := root.WriteFile(schemacontract.CatalogFile, data, 0o600); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", filepath.Join(root.Name(), schemacontract.CatalogFile))
	return nil
}
