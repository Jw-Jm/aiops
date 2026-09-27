package bundle

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/klauspost/compress/zstd"
)

var payloadDirectories = []string{"oci", "charts", "binaries", "sbom", "licenses", "sources", "profiles"}

type ExtractionLimits struct {
	MaxFiles int
	MaxBytes int64
}

type ExtractionReport struct {
	Files      []PayloadFile
	FileCount  int
	TotalBytes int64
}

// safeExtract is intentionally package-private so extraction can only follow
// Verify's signature and complete digest checks inside this Bundle boundary.
func safeExtract(ctx context.Context, compressed io.Reader, destination string, limits ExtractionLimits) (report ExtractionReport, err error) {
	if limits.MaxFiles < 1 || limits.MaxFiles > 100000 || limits.MaxBytes < 1 {
		return ExtractionReport{}, errors.New("extraction limits must set maxFiles and maxBytes")
	}
	if destination == "" {
		return ExtractionReport{}, errors.New("extraction destination is required")
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		return ExtractionReport{}, fmt.Errorf("create extraction destination: %w", err)
	}
	completed := false
	defer func() {
		if !completed {
			_ = os.RemoveAll(destination)
		}
	}()
	root, err := os.OpenRoot(destination)
	if err != nil {
		return ExtractionReport{}, fmt.Errorf("open extraction root: %w", err)
	}
	defer root.Close()
	for _, directory := range payloadDirectories {
		if err := root.Mkdir(directory, 0o700); err != nil {
			return ExtractionReport{}, fmt.Errorf("create payload directory %q: %w", directory, err)
		}
	}
	maxWindow := uint64(limits.MaxBytes)
	if maxWindow < 1<<20 {
		maxWindow = 1 << 20
	}
	if maxWindow > 1<<30 {
		maxWindow = 1 << 30
	}
	decoder, err := zstd.NewReader(contextInput{ctx: ctx, reader: compressed}, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(maxWindow))
	if err != nil {
		return ExtractionReport{}, fmt.Errorf("open zstd payload: %w", err)
	}
	defer decoder.Close()
	streamLimit := safeStreamLimit(limits)
	limited := &io.LimitedReader{R: decoder, N: streamLimit + 1}
	archive := tar.NewReader(limited)
	seen := make(map[string]bool)
	var totalBytes int64
	entryCount := 0
	directoryCount := 0
	fileCount := 0
	for {
		if err := ctx.Err(); err != nil {
			return ExtractionReport{}, err
		}
		header, nextErr := archive.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return ExtractionReport{}, fmt.Errorf("read tar payload: %w", nextErr)
		}
		entryCount++
		if entryCount > limits.MaxFiles+4096 {
			return ExtractionReport{}, errors.New("archive entry count exceeds the structural safety bound")
		}
		allowRoot := header.Typeflag == tar.TypeDir
		entryPath, pathErr := cleanArchivePath(header.Name, allowRoot)
		if pathErr != nil {
			return ExtractionReport{}, pathErr
		}
		if seen[entryPath] {
			return ExtractionReport{}, fmt.Errorf("duplicate archive path %q", entryPath)
		}
		seen[entryPath] = true
		switch header.Typeflag {
		case tar.TypeDir:
			directoryCount++
			if directoryCount > 4096 {
				return ExtractionReport{}, errors.New("archive directory count exceeds the structural safety bound")
			}
			if header.Size != 0 {
				return ExtractionReport{}, fmt.Errorf("directory %q has a non-zero size", entryPath)
			}
			if !isPayloadRoot(entryPath) {
				if err := root.MkdirAll(filepath.FromSlash(entryPath), 0o700); err != nil {
					return ExtractionReport{}, fmt.Errorf("create directory %q: %w", entryPath, err)
				}
			}
		case tar.TypeReg, tar.TypeRegA:
			fileCount++
			if fileCount > limits.MaxFiles {
				return ExtractionReport{}, errors.New("archive file count exceeds maxFiles")
			}
			if isPayloadRoot(entryPath) {
				return ExtractionReport{}, fmt.Errorf("payload root %q cannot be a regular file", entryPath)
			}
			if header.Size < 0 || header.Size > limits.MaxBytes-totalBytes {
				return ExtractionReport{}, errors.New("archive uncompressed bytes exceed maxBytes")
			}
			parent := path.Dir(entryPath)
			if parent != "." {
				if err := root.MkdirAll(filepath.FromSlash(parent), 0o700); err != nil {
					return ExtractionReport{}, fmt.Errorf("create parent directory for %q: %w", entryPath, err)
				}
			}
			file, err := root.OpenFile(filepath.FromSlash(entryPath), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				return ExtractionReport{}, fmt.Errorf("create payload file %q: %w", entryPath, err)
			}
			hash := sha256.New()
			written, copyErr := io.CopyN(io.MultiWriter(file, hash), archive, header.Size)
			closeErr := file.Close()
			if copyErr != nil {
				return ExtractionReport{}, fmt.Errorf("extract payload file %q: %w", entryPath, copyErr)
			}
			if closeErr != nil {
				return ExtractionReport{}, fmt.Errorf("close payload file %q: %w", entryPath, closeErr)
			}
			if written != header.Size {
				return ExtractionReport{}, fmt.Errorf("payload file %q is truncated", entryPath)
			}
			if header.Mode&0o111 != 0 {
				if err := root.Chmod(filepath.FromSlash(entryPath), 0o700); err != nil {
					return ExtractionReport{}, fmt.Errorf("set executable mode for %q: %w", entryPath, err)
				}
			}
			totalBytes += written
			report.Files = append(report.Files, PayloadFile{
				Path: entryPath, Digest: "sha256:" + hex.EncodeToString(hash.Sum(nil)), Size: written, Kind: pathKind(entryPath),
			})
		default:
			return ExtractionReport{}, fmt.Errorf("archive entry %q has unsupported type %d", entryPath, header.Typeflag)
		}
	}
	remaining, err := io.Copy(io.Discard, limited)
	if err != nil {
		return ExtractionReport{}, fmt.Errorf("finish zstd payload: %w", err)
	}
	if limited.N == 0 {
		return ExtractionReport{}, errors.New("decompressed archive stream exceeds its safety bound")
	}
	if remaining != 0 {
		return ExtractionReport{}, errors.New("tar payload contains trailing data after its end marker")
	}
	if len(report.Files) == 0 {
		return ExtractionReport{}, errors.New("archive contains no regular files")
	}
	sort.Slice(report.Files, func(i, j int) bool { return report.Files[i].Path < report.Files[j].Path })
	report.FileCount = fileCount
	report.TotalBytes = totalBytes
	completed = true
	return report, nil
}

func safeStreamLimit(limits ExtractionLimits) int64 {
	if limits.MaxBytes > (1<<62)-int64(limits.MaxFiles+4096)*1024-(1<<20) {
		return 1 << 62
	}
	return limits.MaxBytes + int64(limits.MaxFiles+4096)*1024 + (1 << 20)
}

func cleanArchivePath(value string, allowRoot bool) (string, error) {
	if value == "" || strings.ContainsAny(value, "\\\x00") || strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("unsafe archive path %q", value)
	}
	trimmed := strings.TrimSuffix(value, "/")
	cleaned := path.Clean(trimmed)
	if trimmed == "" || cleaned != trimmed || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("unsafe archive path %q", value)
	}
	if isPayloadRoot(cleaned) && allowRoot {
		return cleaned, nil
	}
	if pathKind(cleaned) == "" {
		return "", fmt.Errorf("archive path %q is outside the approved payload directories", value)
	}
	return cleaned, nil
}

func isPayloadRoot(value string) bool {
	for _, root := range payloadDirectories {
		if value == root {
			return true
		}
	}
	return false
}

type contextInput struct {
	ctx    context.Context
	reader io.Reader
}

func (input contextInput) Read(buffer []byte) (int, error) {
	if err := input.ctx.Err(); err != nil {
		return 0, err
	}
	return input.reader.Read(buffer)
}
