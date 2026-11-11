package release

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
	"testing"
	"time"
)

func TestArchivesAreDeterministicAndPreserveFiles(t *testing.T) {
	files := []File{{Name: "README.md", Data: []byte("read me"), Mode: 0644}, {Name: "bearforge", Data: []byte("executable"), Mode: 0755}}
	stamp := time.Unix(1700000000, 0).UTC()
	for _, format := range []string{"zip", "tar.gz"} {
		t.Run(format, func(t *testing.T) {
			var first, second bytes.Buffer
			if err := Archive(&first, format, files, stamp); err != nil {
				t.Fatal(err)
			}
			if err := Archive(&second, format, []File{files[1], files[0]}, stamp); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(first.Bytes(), second.Bytes()) {
				t.Fatal("archive depends on input order or wall-clock time")
			}
			seen := map[string]string{}
			if format == "zip" {
				reader, err := zip.NewReader(bytes.NewReader(first.Bytes()), int64(first.Len()))
				if err != nil {
					t.Fatal(err)
				}
				for _, file := range reader.File {
					stream, err := file.Open()
					if err != nil {
						t.Fatal(err)
					}
					data, err := io.ReadAll(stream)
					if err != nil {
						t.Fatal(err)
					}
					if err := stream.Close(); err != nil {
						t.Fatal(err)
					}
					seen[file.Name] = string(data)
					if file.Name == "bearforge" && file.Mode().Perm() != 0755 {
						t.Fatal("executable mode lost")
					}
				}
			} else {
				compressed, err := gzip.NewReader(bytes.NewReader(first.Bytes()))
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = compressed.Close() }()
				reader := tar.NewReader(compressed)
				for {
					header, err := reader.Next()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					data, err := io.ReadAll(reader)
					if err != nil {
						t.Fatal(err)
					}
					seen[header.Name] = string(data)
					if header.Name == "bearforge" && header.Mode != 0755 {
						t.Fatal("executable mode lost")
					}
				}
			}
			if seen["README.md"] != "read me" || seen["bearforge"] != "executable" {
				t.Fatalf("archive contents changed: %+v", seen)
			}
		})
	}
}

func TestArchiveAndVersionValidationRejectUnsafeNames(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", `C:\escape`, "a\nfile"} {
		var output bytes.Buffer
		if err := Archive(&output, "zip", []File{{Name: name, Data: []byte("data")}}, time.Unix(0, 0)); err == nil {
			t.Fatalf("unsafe archive path accepted: %q", name)
		}
	}
	for _, version := range []string{"v../escape", "v0.2.0;command", "v01.2.3", "v0.2.0-01"} {
		if ValidVersion(version) {
			t.Fatalf("unsafe/invalid version accepted: %s", version)
		}
	}
	for _, version := range []string{"v0.2.0", "v1.2.3-rc.1", "v0.2.0-snapshot"} {
		if !ValidVersion(version) {
			t.Fatalf("valid version refused: %s", version)
		}
	}
}

func TestChecksumsCoverExactArtifactBytesInStableOrder(t *testing.T) {
	files := map[string][]byte{"b.zip": []byte("two"), "a.tar.gz": []byte("one")}
	manifest, err := Checksums(files)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(manifest)), "\n")
	if len(lines) != 2 || !strings.HasSuffix(lines[0], "  a.tar.gz") {
		t.Fatalf("unstable checksums: %s", manifest)
	}
	for _, line := range lines {
		sum, name, ok := strings.Cut(line, "  ")
		if !ok {
			t.Fatal("invalid checksum format")
		}
		expected := sha256.Sum256(files[name])
		if sum != hex.EncodeToString(expected[:]) {
			t.Fatal("checksum does not cover exact bytes")
		}
	}
}
