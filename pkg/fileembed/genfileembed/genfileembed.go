/*
Copyright 2012 Google Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// The genfileembed command embeds resources into Go files, to eliminate run-time
// dependencies on files on the filesystem.
package main

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"encoding/base64"
	"flag"
	"fmt"
	"go/parser"
	"go/printer"
	"go/token"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"camlistore.org/pkg/rollsum"
)

var (
	processAll = flag.Bool("all", false, "process all files (if false, only process modified files)")

	fileEmbedPkgPath = flag.String("fileembed-package", "camlistore.org/pkg/fileembed", "the Go package name for fileembed. If you have vendored fileembed (e.g. with goven), you can use this flag to ensure that generated code imports the vendored package.")

	chunkThreshold = flag.Int64("chunk-threshold", 0, "If non-zero, the maximum size of a file before it's cut up into content-addressable chunks with a rolling checksum")
	chunkPackage   = flag.String("chunk-package", "", "Package to hold chunks")
)

const (
	maxUncompressed = 50 << 10 // 50KB
	// Threshold ratio for compression.
	// Files which don't compress at least as well are kept uncompressed.
	zRatio = 0.5
)

func usage() {
	fmt.Fprintf(os.Stderr, "usage: genfileembed [flags] [<dir>]\n")
	flag.PrintDefaults()
	os.Exit(2)
}

func NoSlash(s string) string {
    return strings.Replace(s, "/", "_", -1)
}


func main() {
	flag.Usage = usage
	flag.Parse()

	dir := "."
	switch flag.NArg() {
	case 0:
	case 1:
		dir = flag.Arg(0)
		if err := os.Chdir(dir); err != nil {
			log.Fatalf("chdir(%q) = %v", dir, err)
		}
	default:
		flag.Usage()
	}

	pkgName, _, fileEmbedModTime, err := parseFileEmbed()
	if err != nil {
		log.Fatalf("Error parsing %s/fileembed.go: %v", dir, err)
	}

    cwd, err := os.Getwd()
    if err != nil {
        log.Fatal(err)
    }
    lcwd := len(cwd)+1

    err = filepath.Walk(dir, func(fileName string, fi os.FileInfo, err error) error {
        log.Println("walk-->", fileName, fi.IsDir())
        if fi.IsDir() {
            return nil
        }
        fileName = fileName[lcwd:]
        if strings.HasPrefix(fileName, "zembed") {
            return nil
        }
		embedName := "zembed_" + NoSlash(fileName) + ".go"
        log.Println("embed name is", embedName)
		zfi, zerr := os.Stat(embedName)
		genFile := func() bool {
			if *processAll || zerr != nil {
				return true
			}
			if zfi.ModTime().Before(fi.ModTime()) {
				return true
			}
			if zfi.ModTime().Before(fileEmbedModTime) {
				return true
			}
			return false
		}
		if !genFile() {
			return nil
		}
		log.Printf("Updating %s (package %s)", filepath.Join(dir, embedName), pkgName)

		bs, err := ioutil.ReadFile(fileName)
		if err != nil {
			log.Fatal(err)
		}

		zb, fileSize := compressFile(bytes.NewReader(bs))
		ratio := float64(len(zb)) / float64(fileSize)
		byteStreamType := ""
		var qb []byte // quoted string, or Go expression evaluating to a string
		var imports string
		if *chunkThreshold > 0 && int64(len(bs)) > *chunkThreshold {
			byteStreamType = "fileembed.Multi"
			qb = chunksOf(bs)
			if *chunkPackage == "" {
				log.Fatalf("Must provide a --chunk-package value with --chunk-threshold")
			}
			imports = fmt.Sprintf("import chunkpkg \"%s\"\n", *chunkPackage)
		} else if fileSize < maxUncompressed || ratio > zRatio {
			byteStreamType = "fileembed.String"
			qb = quote(bs)
		} else {
			byteStreamType = "fileembed.ZlibCompressedBase64"
			qb = quote([]byte(base64.StdEncoding.EncodeToString(zb)))
		}

		var b bytes.Buffer
		fmt.Fprintf(&b, "// THIS FILE IS AUTO-GENERATED FROM %s\n", fileName)
		fmt.Fprintf(&b, "// DO NOT EDIT.\n\n")
		fmt.Fprintf(&b, "package %s\n\n", pkgName)
		fmt.Fprintf(&b, "import \"time\"\n\n")
		fmt.Fprintf(&b, "import \""+*fileEmbedPkgPath+"\"\n\n")
		b.WriteString(imports)
		fmt.Fprintf(&b, "func init() {\n\tFiles.Add(%q, %d, time.Unix(0, %d), %s(%s));\n}\n",
			fileName, fileSize, fi.ModTime().UnixNano(), byteStreamType, qb)

		// gofmt it
		fset := token.NewFileSet()
		ast, err := parser.ParseFile(fset, "", b.Bytes(), parser.ParseComments)
		if err != nil {
			log.Fatal(err)
		}

		var clean bytes.Buffer
		config := &printer.Config{
			Mode:     printer.TabIndent | printer.UseSpaces,
			Tabwidth: 8,
		}
		err = config.Fprint(&clean, fset, ast)
		if err != nil {
			log.Fatal(err)
		}

		if err := writeFileIfDifferent(embedName, clean.Bytes()); err != nil {
			log.Fatal(err)
		}
        return nil
	})
    if err != nil {
        log.Fatal(err)
    }
}

func writeFileIfDifferent(filename string, contents []byte) error {
	fi, err := os.Stat(filename)
	if err == nil && fi.Size() == int64(len(contents)) && contentsEqual(filename, contents) {
		return nil
	}
	return ioutil.WriteFile(filename, contents, 0644)
}

func contentsEqual(filename string, contents []byte) bool {
	got, err := ioutil.ReadFile(filename)
	if err != nil {
		return false
	}
	return bytes.Equal(got, contents)
}

func compressFile(r io.Reader) ([]byte, int64) {
	var zb bytes.Buffer
	w := zlib.NewWriter(&zb)
	n, err := io.Copy(w, r)
	if err != nil {
		log.Fatal(err)
	}
	w.Close()
	return zb.Bytes(), n
}

func quote(bs []byte) []byte {
	var qb bytes.Buffer
	qb.WriteByte('"')
	run := 0
	for _, b := range bs {
		if b == '\n' {
			qb.WriteString(`\n`)
		}
		if b == '\n' || run > 80 {
			qb.WriteString("\" +\n\t\"")
			run = 0
		}
		if b == '\n' {
			continue
		}
		run++
		if b == '\\' {
			qb.WriteString(`\\`)
			continue
		}
		if b == '"' {
			qb.WriteString(`\"`)
			continue
		}
		if (b >= 32 && b <= 126) || b == '\t' {
			qb.WriteByte(b)
			continue
		}
		fmt.Fprintf(&qb, "\\x%02x", b)
	}
	qb.WriteByte('"')
	return qb.Bytes()
}

func matchingFiles(p *regexp.Regexp) []string {
	var f []string
	d, err := os.Open(".")
	if err != nil {
		log.Fatal(err)
	}
	defer d.Close()
	names, err := d.Readdirnames(-1)
	if err != nil {
		log.Fatal(err)
	}
	for _, n := range names {
		if strings.HasPrefix(n, "zembed_") {
			continue
		}
		if p.MatchString(n) {
			f = append(f, n)
		}
	}
	return f
}

func parseFileEmbed() (pkgName string, filePattern *regexp.Regexp, modTime time.Time, err error) {
	fe, err := os.Open("fileembed.go")
	if err != nil {
		return
	}
	defer fe.Close()

	fi, err := fe.Stat()
	if err != nil {
		return
	}
	modTime = fi.ModTime()

	fs := token.NewFileSet()
	astf, err := parser.ParseFile(fs, "fileembed.go", fe, parser.PackageClauseOnly|parser.ParseComments)
	if err != nil {
		return
	}
	pkgName = astf.Name.Name

	if astf.Doc == nil {
		err = fmt.Errorf("no package comment before the %q line", "package "+pkgName)
		return
	}

	pkgComment := astf.Doc.Text()
	findPattern := regexp.MustCompile(`(?m)^#fileembed\s+pattern\s+(\S+)\s*$`)
	m := findPattern.FindStringSubmatch(pkgComment)
	if m == nil {
		err = fmt.Errorf("package comment lacks line of form: #fileembed pattern <pattern>")
		return
	}
	pattern := m[1]
	filePattern, err = regexp.Compile(pattern)
	if err != nil {
		err = fmt.Errorf("bad regexp %q: %v", pattern, err)
		return
	}
	return
}

// chunksOf takes a (presumably large) file's uncompressed input,
// rolling-checksum splits it into ~514 byte chunks, compresses each,
// base64s each, and writes chunk files out, with each file just
// defining an exported fileembed.Opener variable named C<xxxx> where
// xxxx is the first 8 lowercase hex digits of the SHA-1 of the chunk
// value pre-compression.  The return value is a Go expression
// referencing each of those chunks concatenated together.
func chunksOf(in []byte) (stringExpression []byte) {
	var multiParts [][]byte
	rs := rollsum.New()
	const nBits = 9 // ~512 byte chunks
	last := 0
	for i, b := range in {
		rs.Roll(b)
		if rs.OnSplitWithBits(nBits) || i == len(in)-1 {
			raw := in[last : i+1] // inclusive
			last = i + 1
			s1 := sha1.New()
			s1.Write(raw)
			sha1hex := fmt.Sprintf("%x", s1.Sum(nil))[:8]
			writeChunkFile(sha1hex, raw)
			multiParts = append(multiParts, []byte(fmt.Sprintf("chunkpkg.C%s", sha1hex)))
		}
	}
	return bytes.Join(multiParts, []byte(",\n\t"))
}

func writeChunkFile(hex string, raw []byte) {
	path := os.Getenv("GOPATH")
	if path == "" {
		log.Fatalf("No GOPATH set")
	}
	path = filepath.SplitList(path)[0]
	file := filepath.Join(path, "src", filepath.FromSlash(*chunkPackage), "chunk_"+hex+".go")
	zb, _ := compressFile(bytes.NewReader(raw))
	var buf bytes.Buffer
	buf.WriteString("// THIS FILE IS AUTO-GENERATED. SEE README.\n\n")
	buf.WriteString("package chunkpkg\n")
	buf.WriteString("import \"" + *fileEmbedPkgPath + "\"\n\n")
	fmt.Fprintf(&buf, "var C%s fileembed.Opener\n\nfunc init() { C%s = fileembed.ZlibCompressedBase64(%s)\n }\n",
		hex,
		hex,
		quote([]byte(base64.StdEncoding.EncodeToString(zb))))
	err := writeFileIfDifferent(file, buf.Bytes())
	if err != nil {
		log.Fatalf("Error writing chunk %s to %v: %v", hex, file, err)
	}
}
