package snapshot

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/SohamD1/bellwether/internal/features"
	"github.com/parquet-go/parquet-go"
)

// WriteParquet validates rows, writes them to a temporary sibling, and replaces
// path only after the complete Parquet file has been closed and synchronized.
// It returns the logical data snapshot ID, which is independent of file bytes.
func WriteParquet(path string, rows []features.Snapshot) (string, error) {
	id, err := ComputeID(rows)
	if err != nil {
		return "", err
	}

	dir := filepath.Dir(path)
	temporary, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("snapshot: create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	writer := parquet.NewGenericWriter[parquetRow](temporary)
	if _, err := writer.Write(toParquetRows(rows)); err != nil {
		_ = writer.Close()
		_ = temporary.Close()
		return "", fmt.Errorf("snapshot: write Parquet: %w", err)
	}
	if err := writer.Close(); err != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("snapshot: close Parquet writer: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("snapshot: synchronize temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("snapshot: close temporary file: %w", err)
	}
	if err := replaceFile(temporaryPath, path); err != nil {
		return "", fmt.Errorf("snapshot: replace destination: %w", err)
	}
	return id, nil
}

// ReadParquet reads and validates logical feature rows from path.
func ReadParquet(path string) ([]features.Snapshot, error) {
	rows, err := parquet.ReadFile[parquetRow](path)
	if err != nil {
		return nil, fmt.Errorf("snapshot: read Parquet: %w", err)
	}
	converted := fromParquetRows(rows)
	if err := validate(converted); err != nil {
		return nil, err
	}
	return converted, nil
}
