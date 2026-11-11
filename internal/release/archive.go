// Package release builds deterministic distributable artifacts without
// publishing, tagging or signing with a local production key.
package release

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
)

type File struct {
	Name string
	Data []byte
	Mode fs.FileMode
}

var versionPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

func ValidVersion(value string) bool {
	if !versionPattern.MatchString(value) {
		return false
	}
	core, _, _ := strings.Cut(value, "+")
	if _, prerelease, ok := strings.Cut(core, "-"); ok {
		for _, part := range strings.Split(prerelease, ".") {
			if len(part) > 1 && part[0] == '0' && !strings.ContainsFunc(part, func(r rune) bool { return r < '0' || r > '9' }) {
				return false
			}
		}
	}
	return true
}

func safeArchiveName(name string) bool {
	return name != "" && name != "." && path.Clean(name) == name && !strings.HasPrefix(name, "/") && !strings.HasPrefix(name, "../") && !strings.ContainsAny(name, `\:`) && !strings.ContainsFunc(name, unicode.IsControl)
}

func Archive(out io.Writer, format string, files []File, stamp time.Time) error {
	files = slices.Clone(files)
	slices.SortFunc(files, func(a, b File) int { return strings.Compare(a.Name, b.Name) })
	for i, file := range files {
		if !safeArchiveName(file.Name) || (i > 0 && files[i-1].Name == file.Name) {
			return fmt.Errorf("unsafe or duplicate archive name %q", file.Name)
		}
	}
	stamp = stamp.UTC().Truncate(time.Second)
	switch format {
	case "zip":
		writer := zip.NewWriter(out)
		for _, file := range files {
			header := &zip.FileHeader{Name: file.Name, Method: zip.Deflate}
			header.Modified = stamp
			header.SetMode(file.Mode.Perm())
			entry, err := writer.CreateHeader(header)
			if err != nil {
				return errors.Join(err, writer.Close())
			}
			if _, err := entry.Write(file.Data); err != nil {
				return errors.Join(err, writer.Close())
			}
		}
		return writer.Close()
	case "tar.gz":
		compressed := gzip.NewWriter(out)
		compressed.ModTime = stamp
		compressed.OS = 255
		writer := tar.NewWriter(compressed)
		for _, file := range files {
			header := &tar.Header{Name: file.Name, Mode: int64(file.Mode.Perm()), Size: int64(len(file.Data)), ModTime: stamp, Typeflag: tar.TypeReg, Format: tar.FormatPAX}
			if err := writer.WriteHeader(header); err != nil {
				return errors.Join(err, writer.Close(), compressed.Close())
			}
			if _, err := writer.Write(file.Data); err != nil {
				return errors.Join(err, writer.Close(), compressed.Close())
			}
		}
		return errors.Join(writer.Close(), compressed.Close())
	default:
		return fmt.Errorf("unsupported archive format %q", format)
	}
}

func Checksums(files map[string][]byte) ([]byte, error) {
	names := make([]string, 0, len(files))
	for name := range files {
		if !safeArchiveName(name) || strings.ContainsAny(name, "/ \t") {
			return nil, fmt.Errorf("unsafe checksum filename %q", name)
		}
		names = append(names, name)
	}
	slices.Sort(names)
	var out strings.Builder
	for _, name := range names {
		sum := sha256.Sum256(files[name])
		fmt.Fprintf(&out, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	return []byte(out.String()), nil
}
