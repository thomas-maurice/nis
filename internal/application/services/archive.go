package services

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// extractArchive auto-detects an archive format (zip, tar.gz, tar.bz2) in raw
// bytes and extracts it into a fresh temp directory, returning the directory
// path. The caller owns the temp directory and must `os.RemoveAll` it. Lives
// next to the NSC import code because it's the only consumer today, but is
// intentionally package-level (not a method on ExportService) so the archive
// detection is testable without standing up the whole service.
func extractArchive(archiveData []byte) (string, error) {
	// Create temp directory
	tempDir, err := os.MkdirTemp("", "nsc-import-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Try to detect and extract the archive type
	reader := bytes.NewReader(archiveData)

	// Try ZIP first
	zipReader, err := zip.NewReader(reader, int64(len(archiveData)))
	if err == nil {
		for _, file := range zipReader.File {
			if err := extractZipFile(file, tempDir); err != nil {
				_ = os.RemoveAll(tempDir)
				return "", fmt.Errorf("failed to extract zip file: %w", err)
			}
		}
		return tempDir, nil
	}

	// Try gzip + tar
	_, _ = reader.Seek(0, io.SeekStart)
	gzipReader, err := gzip.NewReader(reader)
	if err == nil {
		defer func() { _ = gzipReader.Close() }()
		if err := extractTar(gzipReader, tempDir); err == nil {
			return tempDir, nil
		}
		_ = os.RemoveAll(tempDir)
		return "", fmt.Errorf("failed to extract tar.gz: %w", err)
	}

	// Try bzip2 + tar
	_, _ = reader.Seek(0, io.SeekStart)
	bz2Reader := bzip2.NewReader(reader)
	if err := extractTar(bz2Reader, tempDir); err == nil {
		return tempDir, nil
	}

	_ = os.RemoveAll(tempDir)
	return "", fmt.Errorf("unsupported archive format (supported: .zip, .tar.gz, .tar.bz2)")
}

// extractZipFile extracts a single file entry from a ZIP archive.
func extractZipFile(file *zip.File, destDir string) error {
	rc, err := file.Open()
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()

	path := filepath.Join(destDir, file.Name)

	if file.FileInfo().IsDir() {
		return os.MkdirAll(path, file.Mode())
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	_, err = io.Copy(f, rc)
	return err
}

// extractTar reads a tar stream and writes each entry beneath destDir.
func extractTar(reader io.Reader, destDir string) error {
	tarReader := tar.NewReader(reader)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		path := filepath.Join(destDir, header.Name)

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				return err
			}
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tarReader); err != nil {
				_ = f.Close()
				return err
			}
			_ = f.Close()
		}
	}

	return nil
}
