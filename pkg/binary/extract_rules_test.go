package binary

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// tarPaths are the paths that install a binary from a tar archive: a preset
// and a release asset, each as a tar.gz and a tar.xz.
var tarPaths = []archivePath{
	{name: "a tar.gz preset", kind: "tar.gz"},
	{name: "a tar.xz preset", kind: "tar.xz"},
	{name: "a tar.gz asset", kind: "tar.gz", asset: true},
	{name: "a tar.xz asset", kind: "tar.xz", asset: true},
}

// tarMembers is the tar archive (not compressed) of the members, with the
// mode rule of archiveOf.
func tarMembers(t *testing.T, members ...member) []byte {
	t.Helper()
	var entries []tarEntry
	for _, m := range members {
		mode := int64(0755)
		if m.mode != 0 {
			mode = int64(m.mode.Perm())
		}
		entries = append(entries, tarEntry{name: m.name, mode: mode, content: []byte(m.content)})
	}
	return tarOf(t, entries...)
}

// archiveFor is archiveOf for every kind: "tar.gz", "tar.xz" or "zip".
func archiveFor(t *testing.T, kind string, members ...member) []byte {
	t.Helper()
	if kind == "tar.xz" {
		return compressed(t, kind, tarMembers(t, members...))
	}
	return archiveOf(t, kind, members...)
}

// zipNoModes is a zip archive of the members without Unix modes: the
// creator of each member is MS-DOS, as a zip from Windows tools.
func zipNoModes(t *testing.T, members ...member) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, m := range members {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: m.name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, m.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestExtract_DriveNames: an archive that holds a regular file with a drive
// name is refused on every path, as an absolute name is.
func TestExtract_DriveNames(t *testing.T) {
	for _, p := range archivePaths {
		for _, name := range []string{"C:/tool", `C:\tool`, "c:tool", `z:\bin\tool`} {
			t.Run(p.name+", "+name, func(t *testing.T) {
				got, err := installFrom(t, p, archiveOf(t, p.kind, member{name: name, content: "other\n"}, member{name: "tool", content: "tool v2\n"}))
				wantRefused(t, got, err, "refusing tool."+p.kind+": the file name "+strconv.Quote(name)+` is absolute, names a drive or has a ".." element`, false)
			})
		}
	}
	t.Run("a colon after the first letter is not a drive", func(t *testing.T) {
		for _, name := range []string{"ab:tool", "1:tool", "dir/c:tool"} {
			if unsafeName(name) {
				t.Errorf("unsafeName(%q) is true", name)
			}
		}
	})
}

// TestExtract_ExecRule: one rule on every path. A file whose base name is
// the binary is the binary, with or without an execute bit. The execute bit
// counts only for the fallback of the asset paths, when no file matches by
// name. Two or more files that match by name refuse the archive, whatever
// their modes.
func TestExtract_ExecRule(t *testing.T) {
	bin := member{name: "tool", content: "tool v2\n"}
	completion := member{name: "completions/bash/tool", content: "complete -F _tool tool\n", mode: 0644}
	for _, p := range append(archivePaths, tarPaths[1], tarPaths[3]) {
		archive := "tool." + p.kind
		t.Run(p.name+", the binary without an execute bit beside an executable helper", func(t *testing.T) {
			got, err := installFrom(t, p, archiveFor(t, p.kind,
				member{name: "bin/tool", content: "tool v2\n", mode: 0644}, member{name: "bin/helper", content: "a larger helper binary\n"}))
			wantInstalled(t, got, err, "tool v2\n")
		})
		t.Run(p.name+", one match without an execute bit", func(t *testing.T) {
			got, err := installFrom(t, p, archiveFor(t, p.kind, member{name: "tool", content: "tool v2\n", mode: 0644}))
			wantInstalled(t, got, err, "tool v2\n")
		})
		t.Run(p.name+", the binary and a completion script of the same name", func(t *testing.T) {
			got, err := installFrom(t, p, archiveFor(t, p.kind, completion, bin))
			wantRefused(t, got, err, "refusing "+archive+`: 2 files in it match the binary tool: "completions/bash/tool", "tool"`, false)
		})
		t.Run(p.name+", two executables named tool", func(t *testing.T) {
			got, err := installFrom(t, p, archiveFor(t, p.kind, member{name: "tool", content: "tool a\n"}, member{name: "tool", content: "tool b\n"}, completion))
			wantRefused(t, got, err, "refusing "+archive+`: 3 files in it match the binary tool: "tool", "tool", "completions/bash/tool"`, false)
		})
	}
	for _, p := range []archivePath{{name: "a zip preset", kind: "zip"}, {name: "a zip asset", kind: "zip", asset: true}} {
		t.Run(p.name+" without Unix modes, two files named tool", func(t *testing.T) {
			got, err := installFrom(t, p, zipNoModes(t, bin, member{name: "tool", content: "not this\n"}))
			wantRefused(t, got, err, `refusing tool.zip: 2 files in it match the binary tool: "tool", "tool"`, false)
		})
	}
}

// TestZipExec: a zip file carries a Unix mode only when its creator is Unix
// or macOS and the mode has permission bits.
func TestZipExec(t *testing.T) {
	for _, tc := range []struct {
		name    string
		creator uint16
		mode    fs.FileMode
		want    execMode
	}{
		{"Unix, 0755", zipCreatorUnix, 0755, execSet},
		{"Unix, 0644", zipCreatorUnix, 0644, execUnset},
		{"Unix, 0100", zipCreatorUnix, 0100, execSet},
		{"Unix, no permission bits", zipCreatorUnix, 0, execUnknown},
		{"macOS, 0755", zipCreatorMacOS, 0755, execSet},
		{"macOS, 0644", zipCreatorMacOS, 0644, execUnset},
		{"MS-DOS, 0755", 0, 0755, execUnknown},
		{"NTFS, 0755", 10, 0755, execUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fh := zip.FileHeader{Name: "tool"}
			fh.SetMode(tc.mode)
			fh.CreatorVersion = tc.creator<<8 | fh.CreatorVersion&0xff
			if got := zipExec(&zip.File{FileHeader: fh}); got != tc.want {
				t.Errorf("zipExec = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestExtract_ZipFallbackMayRun: on the zip asset path, without a match by
// name, the fallback takes the largest file that may run. With Unix modes, a
// larger file without an execute bit is not a candidate. Without Unix modes,
// every regular file is one, so the largest file installs (a known limit).
func TestExtract_ZipFallbackMayRun(t *testing.T) {
	readme := member{name: "README.md", content: "a readme that is larger than the binary\n", mode: 0644}
	binary := member{name: "tool-linux-amd64", content: "tool v2\n"}
	for _, p := range []archivePath{{name: "a zip asset", kind: "zip", asset: true}, tarPaths[2], tarPaths[3]} {
		t.Run(p.name+" with Unix modes", func(t *testing.T) {
			got, err := installFrom(t, p, archiveFor(t, p.kind, readme, binary))
			wantInstalled(t, got, err, "tool v2\n")
		})
	}
	t.Run("a zip asset without Unix modes", func(t *testing.T) {
		got, err := installFrom(t, archivePath{name: "a zip asset", kind: "zip", asset: true}, zipNoModes(t, readme, binary))
		wantInstalled(t, got, err, readme.content)
	})
}

// TestExtract_BackslashSeparator: matching by base name takes "/" and "\" as
// separators, as unsafeName does, on every OS and every path (the zip preset
// included). So bin\tool matches the binary tool and wins over a larger
// file.
func TestExtract_BackslashSeparator(t *testing.T) {
	for _, p := range append(archivePaths, tarPaths[1], tarPaths[3]) {
		t.Run(p.name, func(t *testing.T) {
			got, err := installFrom(t, p, archiveFor(t, p.kind,
				member{name: `bin\tool`, content: "tool v2\n"}, member{name: "other", content: "a larger file that is not the binary\n"}))
			wantInstalled(t, got, err, "tool v2\n")
		})
	}
}

// TestExtract_UnpackLimit: b counts every byte that the decompressor of a
// tar archive gives, a file that it skips included, and refuses an archive
// that unpacks to more than maxUnpackedSize. At the limit, it installs.
func TestExtract_UnpackLimit(t *testing.T) {
	members := []member{{name: "README.md", content: strings.Repeat("r", 4096), mode: 0644}, {name: "tool", content: "tool v2\n"}}
	raw := int64(len(tarMembers(t, members...)))
	for _, p := range tarPaths {
		body := compressed(t, p.kind, tarMembers(t, members...))
		t.Run(p.name+", at the limit", func(t *testing.T) {
			withLimit(t, &maxUnpackedSize, raw)
			got, err := installFrom(t, p, body)
			wantInstalled(t, got, err, "tool v2\n")
		})
		t.Run(p.name+", one byte above the limit", func(t *testing.T) {
			withLimit(t, &maxUnpackedSize, raw-1)
			got, err := installFrom(t, p, body)
			wantRefused(t, got, err, fmt.Sprintf("refusing tool.%s: it unpacks to more than %d bytes, the limit for an archive", p.kind, raw-1), false)
		})
	}
}

// TestExtract_TarHeaderSizeIsCheckedFirst: a tar entry whose header size is
// above the unpack limit refuses the archive by the header, before b reads
// one byte of the entry. The archive holds only 8 bytes of it, so a read
// would fail with io.ErrUnexpectedEOF instead.
func TestExtract_TarHeaderSizeIsCheckedFirst(t *testing.T) {
	for _, p := range tarPaths {
		t.Run(p.name, func(t *testing.T) {
			got, err := installFrom(t, p, compressed(t, p.kind, shortTar(t, "tool", 1<<40, []byte("tool v2\n"))))
			wantRefused(t, got, err, fmt.Sprintf("refusing tool.%s: it unpacks to more than %d bytes, the limit for an archive", p.kind, maxUnpackedSize), false)
		})
	}
}

// TestExtract_EntryLimit: an archive with more than maxEntries entries is
// refused on every path. At the limit, it installs.
func TestExtract_EntryLimit(t *testing.T) {
	old := maxEntries
	maxEntries = 2
	t.Cleanup(func() { maxEntries = old })
	for _, p := range append(archivePaths, tarPaths[1], tarPaths[3]) {
		t.Run(p.name+", at the limit", func(t *testing.T) {
			got, err := installFrom(t, p, archiveFor(t, p.kind, member{name: "README.md", content: "docs\n", mode: 0644}, member{name: "tool", content: "tool v2\n"}))
			wantInstalled(t, got, err, "tool v2\n")
		})
		t.Run(p.name+", above the limit", func(t *testing.T) {
			got, err := installFrom(t, p, archiveFor(t, p.kind,
				member{name: "README.md", content: "docs\n", mode: 0644}, member{name: "tool", content: "tool v2\n"}, member{name: "LICENSE", content: "license\n", mode: 0644}))
			wantRefused(t, got, err, "refusing tool."+p.kind+": it has more than 2 entries, the limit for an archive", false)
		})
	}
}

// TestExtract_AmbiguousListsTenNames: an ambiguity error lists at most
// maxListedNames names, then the count of the others; the count of all the
// matches stays in front.
func TestExtract_AmbiguousListsTenNames(t *testing.T) {
	var members []member
	for i := 0; i < 12; i++ {
		members = append(members, member{name: "tool", content: fmt.Sprintf("tool %d\n", i)})
	}
	want := `12 files in it match the binary tool: ` + strings.Repeat(`"tool", `, 9) + `"tool" and 2 more`
	for _, p := range append(archivePaths, tarPaths[1], tarPaths[3]) {
		t.Run(p.name, func(t *testing.T) {
			got, err := installFrom(t, p, archiveFor(t, p.kind, members...))
			wantRefused(t, got, err, "refusing tool."+p.kind+": "+want, false)
		})
	}
}

// TestExtract_StreamEnd: after the tar archive, b reads the compressed
// stream (one gzip member, or one xz stream) to its end, and refuses the
// archive when the checksum there is bad. b does not read the bytes after
// that stream: zero padding, other bytes, and a second gzip member or xz
// stream, also one that unpacks to more than the unpack limit.
func TestExtract_StreamEnd(t *testing.T) {
	good := tarMembers(t, member{name: "tool", content: "tool v2\n"})
	for _, p := range tarPaths {
		archive := "tool." + p.kind
		kind := map[string]string{"tar.gz": "gzip", "tar.xz": "xz"}[p.kind]
		t.Run(p.name+", a bad checksum at the end of the stream", func(t *testing.T) {
			body := compressed(t, p.kind, good)
			// gzip: the first byte of the CRC-32 in the trailer (8 bytes).
			// xz: the first byte of the CRC-32 of the stream footer (12
			// bytes).
			body[len(body)-map[string]int{"tar.gz": 8, "tar.xz": 12}[p.kind]] ^= 0xff
			got, err := installFrom(t, p, body)
			wantRefused(t, got, err, "refusing "+archive+": the "+kind+" stream after the tar archive is damaged: ", true)
			if p.kind == "tar.gz" && !errors.Is(err, gzip.ErrChecksum) {
				t.Errorf("error %q does not wrap gzip.ErrChecksum", err)
			}
		})
		for _, after := range []struct {
			name  string
			bytes []byte
		}{
			{"zero padding", make([]byte, 1024)},
			{"bytes that are not a stream", []byte("these bytes are not a stream")},
		} {
			t.Run(p.name+", "+after.name+" after the stream", func(t *testing.T) {
				got, err := installFrom(t, p, append(compressed(t, p.kind, good), after.bytes...))
				wantInstalled(t, got, err, "tool v2\n")
			})
		}
		t.Run(p.name+", a second stream above the unpack limit", func(t *testing.T) {
			// The first stream unpacks to the limit. If b read the second
			// stream, the archive would unpack to more than the limit.
			withLimit(t, &maxUnpackedSize, int64(len(good)))
			body := append(compressed(t, p.kind, good), compressed(t, p.kind, make([]byte, 64<<10))...)
			got, err := installFrom(t, p, body)
			wantInstalled(t, got, err, "tool v2\n")
		})
		t.Run(p.name+", a clean end", func(t *testing.T) {
			got, err := installFrom(t, p, compressed(t, p.kind, good))
			wantInstalled(t, got, err, "tool v2\n")
		})
	}
}

// TestExtract_GzipMembers: a tar archive can go on in the next gzip member,
// as it could before the stream-end check: the end of a member is not the
// end of the tar archive, also when it falls between two entries. After the
// member that holds the end of the tar archive, b reads no next member. An
// xz stream must hold the whole tar archive: a tar.xz in two streams is
// refused.
func TestExtract_GzipMembers(t *testing.T) {
	raw := tarMembers(t, member{name: "helper", content: "a larger helper binary\n"}, member{name: "tool", content: "tool v2\n"})
	// helper: a header (0-511) and one block of content (512-1023); then
	// the header of tool at 1024.
	cuts := []struct {
		name string
		at   int
	}{{"between two entries", 1024}, {"inside a header", 1100}, {"inside the content of an entry", 520}}
	for _, p := range tarPaths {
		for _, cut := range cuts {
			t.Run(p.name+", two streams, a cut "+cut.name, func(t *testing.T) {
				body := append(compressed(t, p.kind, raw[:cut.at]), compressed(t, p.kind, raw[cut.at:])...)
				got, err := installFrom(t, p, body)
				if p.kind == "tar.xz" {
					wantRefused(t, got, err, "reading the tar archive tool.tar.xz: xz: unexpected data after stream", true)
					return
				}
				wantInstalled(t, got, err, "tool v2\n")
			})
		}
		if p.kind == "tar.gz" {
			t.Run(p.name+", a tar archive in two members, then a member above the unpack limit", func(t *testing.T) {
				withLimit(t, &maxUnpackedSize, int64(len(raw)))
				body := append(compressed(t, p.kind, raw[:1100]), compressed(t, p.kind, raw[1100:])...)
				body = append(body, compressed(t, p.kind, make([]byte, 64<<10))...)
				got, err := installFrom(t, p, body)
				wantInstalled(t, got, err, "tool v2\n")
			})
		}
	}
}

// TestExtract_MemberNamesAreQuoted: an error that names a file of an archive
// quotes it (%q), so a control character in the name does not reach the
// terminal.
func TestExtract_MemberNamesAreQuoted(t *testing.T) {
	const name = "\x1b[31m/tool"
	noEsc := func(t *testing.T, err error) {
		t.Helper()
		if err != nil && strings.ContainsRune(err.Error(), 0x1b) {
			t.Errorf("error %q holds a raw ESC byte", err)
		}
	}
	for _, p := range append(archivePaths, tarPaths[1], tarPaths[3]) {
		t.Run(p.name+", an empty file", func(t *testing.T) {
			got, err := installFrom(t, p, archiveFor(t, p.kind, member{name: name, content: ""}))
			wantRefused(t, got, err, "extraction of "+strconv.Quote(name)+" from tool."+p.kind+" wrote 0 bytes: ", true)
			noEsc(t, err)
		})
		t.Run(p.name+", an unsafe name", func(t *testing.T) {
			got, err := installFrom(t, p, archiveFor(t, p.kind, member{name: "/\x1b[2Jtool", content: "x\n"}))
			wantRefused(t, got, err, "the file name "+strconv.Quote("/\x1b[2Jtool")+" is absolute", true)
			noEsc(t, err)
		})
	}
	t.Run("a zip asset, a file that cannot be opened", func(t *testing.T) {
		got, err := installFrom(t, archivePath{name: "a zip asset", kind: "zip", asset: true}, unknownMethodZip(t, name, []byte("tool v2\n")))
		wantRefused(t, got, err, "opening "+strconv.Quote(name)+" in tool.zip: ", true)
		noEsc(t, err)
	})
}

// flipFile is an archiveFile that reads the next of its archives at each
// Seek to the start.
type flipFile struct {
	*bytes.Reader
	archives [][]byte
	next     int
}

func (f *flipFile) Seek(offset int64, whence int) (int64, error) {
	if offset == 0 && whence == io.SeekStart && f.next < len(f.archives) {
		f.Reader = bytes.NewReader(f.archives[f.next])
		f.next++
	}
	return f.Reader.Seek(offset, whence)
}

// TestExtractTar_ArchiveChanged: the second read of a tar archive installs
// only the entry that the first read selected. An archive that changed
// between the two reads is refused.
func TestExtractTar_ArchiveChanged(t *testing.T) {
	first := compressed(t, "tar.gz", tarMembers(t, member{name: "tool", content: "tool v2\n"}))
	for name, second := range map[string][]byte{
		"another name": compressed(t, "tar.gz", tarMembers(t, member{name: "other", content: "tool v2\n"})),
		"another size": compressed(t, "tar.gz", tarMembers(t, member{name: "tool", content: "tool v3 is longer\n"})),
	} {
		t.Run(name, func(t *testing.T) {
			b := &Binary{Name: "tool", File: filepath.Join(t.TempDir(), "tool")}
			f := &flipFile{Reader: bytes.NewReader(nil), archives: [][]byte{first, second}}
			err := b.extractFromTarAuto(f, "gz", "tool.tar.gz")
			if want := "refusing tool.tar.gz: the archive changed between two reads"; err == nil || err.Error() != want {
				t.Errorf("error %v, want %q", err, want)
			}
		})
	}
}

// TestAsArchive: a download body that is not a file (fetch.Download always
// gives one for a 200) is refused, not read into memory.
func TestAsArchive(t *testing.T) {
	_, err := asArchive(io.MultiReader(strings.NewReader("data")), "tool.tar.gz")
	if want := "reading tool.tar.gz: the download is not in a file"; err == nil || err.Error() != want {
		t.Errorf("error %v, want %q", err, want)
	}
	if _, err := asArchive(bytes.NewReader(nil), "tool.tar.gz"); err != nil {
		t.Errorf("a *bytes.Reader: %v", err)
	}
}
