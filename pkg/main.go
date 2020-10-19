package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

func findGo() string {
	path, _ := exec.LookPath("go")
	return path
}

var (
	gobin     = flag.String("go", findGo(), "Go binary")
	nobuild   = flag.Bool("nobuild", false, "skip go build")
	noarchive = flag.Bool("noarchive", false, "skip archiving")
)

var targets = []struct{ os, arch string }{
	{"darwin", "amd64"},
	{"freebsd", "amd64"},
	{"linux", "386"},
	{"linux", "amd64"},
	{"linux", "arm"},
	{"linux", "arm64"},
	{"openbsd", "amd64"},
	{"windows", "386"},
	{"windows", "amd64"},
}

const relver = "v0.0.1"

const ldflags = `-buildid= ` +
	`-X decred.org/dcrdex/client/cmd/dexc.appPreRelease=beta ` +
	`-X decred.org/dcrdex/client/cmd/dexc.appBuild= ` +
	`-X decred.org/dcrdex/server/cmd/dcrdex.appPreRelease=beta ` +
	`-X decred.org/dcrdex/server/cmd/dcrdex.appBuild= `

const tags = ""

var tools = []struct{ builddir, outdir string }{
	{"../client/cmd/dexc", "./bin/client/"},
	{"../client/cmd/dexcctl", "./bin/client/"},
	{"../server/cmd/dcrdex", "./bin/server/"},
}

type manifestLine struct {
	name string
	hash [32]byte
}

type manifest []manifestLine

func main() {
	flag.Parse()
	logvers()
	var m manifest
	for i := range targets {
		for j := range tools {
			if *nobuild {
				break
			}
			build(targets[i].os, targets[i].arch, tools[j].builddir, tools[j].outdir)
		}
		if *noarchive {
			continue
		}
		archive(targets[i].os, targets[i].arch, &m)
	}
	if len(m) > 0 {
		writeManifest(m)
	}
}

func logvers() {
	output, err := exec.Command(*gobin, "version").CombinedOutput()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("releasing with %s %s", *gobin, output)
}

func exeName(module, goos string) string {
	isMajor := func(s string) bool {
		for _, v := range s {
			if v < '0' || v > '9' {
				return false
			}
		}
		return len(s) > 0
	}
	exe := path.Base(module)
	// strip /v2+
	if exe[0] == 'v' && isMajor(exe[1:]) {
		exe = path.Base(path.Dir(module))
	}
	if goos == "windows" {
		exe += ".exe"
	}
	return exe
}

// func readasset(builddir string, goargs []string) []byte {
// 	cmd := exec.Command(*gobin, goargs...)
// 	cmd.Dir = builddir
// 	output, err := cmd.Output()
// 	if err != nil {
// 		log.Fatal(err)
// 	}
// 	return output
// }

func build(goos, arch, builddir, out string) {
	out, err := filepath.Abs(filepath.Join(out, goos+"-"+arch, exeName(builddir, goos)))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(out)
	gocmd(goos, arch, builddir, "build", "-trimpath", "-tags", tags, "-o", out, "-ldflags", ldflags)
}

func gocmd(goos, arch, builddir string, args ...string) {
	os.Setenv("GOOS", goos)
	os.Setenv("GOARCH", arch)
	os.Setenv("CGO_ENABLED", "0")
	os.Setenv("GOFLAGS", "")
	if arch == "arm" {
		os.Setenv("GOARM", "7")
	}
	cmd := exec.Command(*gobin, args...)
	cmd.Dir = builddir
	os.MkdirAll(builddir, 0o744)
	fmt.Println(cmd.String())
	output, err := cmd.CombinedOutput()
	if len(output) != 0 {
		log.Printf("go '%s'\n%s", strings.Join(args, `' '`), output)
	}
	if err != nil {
		log.Fatal(err)
	}
}

func archive(goos, arch string, m *manifest) {
	if _, err := os.Stat("archive"); os.IsNotExist(err) {
		err := os.Mkdir("archive", 0777)
		if err != nil {
			log.Fatal(err)
		}
	}
	if goos == "windows" {
		archiveZip(goos, arch, m)
		return
	}
	tarPath := fmt.Sprintf("decred-%s-%s-%s", goos, arch, relver)
	tarFile, err := os.Create(fmt.Sprintf("archive/%s.tar", tarPath))
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("archive: %v", tarFile.Name()+".gz")
	tw := tar.NewWriter(tarFile)
	hdr := &tar.Header{
		Name:     tarPath + "/",
		Typeflag: tar.TypeDir,
		Mode:     0755,
		Format:   tar.FormatPAX,
	}
	err = tw.WriteHeader(hdr)
	if err != nil {
		log.Fatal(err)
	}
	addFile := func(name string, r io.Reader, mode, size int64) {
		hdr := &tar.Header{
			Name:     strings.ReplaceAll(filepath.Join(tarPath, name), `\`, `/`),
			Typeflag: tar.TypeReg,
			Mode:     mode,
			Size:     size,
			Format:   tar.FormatPAX,
		}
		err = tw.WriteHeader(hdr)
		if err != nil {
			log.Fatal(err)
		}
		_, err = io.Copy(tw, r)
		if err != nil {
			log.Fatal(err)
		}
	}
	for i := range tools {
		exe := exeName(tools[i].builddir, goos)
		exePath := filepath.Join("bin", goos+"-"+arch, exe)
		info, err := os.Stat(exePath)
		if err != nil {
			log.Fatal(err)
		}
		file, err := os.Open(exePath)
		if err != nil {
			log.Fatal(err)
		}
		addFile(exe, file, 0755, info.Size())
		file.Close()
	}
	// for _, a := range assets {
	// 	addFile(a.name, bytes.NewBuffer(a.contents), 0644, int64(len(a.contents)))
	// }
	err = tw.Close()
	if err != nil {
		log.Fatal(err)
	}
	zf, err := os.Create(tarFile.Name() + ".gz")
	if err != nil {
		log.Fatal(err)
	}
	hash := sha256.New()
	defer func() {
		name := filepath.Base(tarFile.Name()) + ".gz"
		var sum [32]byte
		copy(sum[:], hash.Sum(nil))
		*m = append(*m, manifestLine{name, sum})
	}()
	w := io.MultiWriter(zf, hash)
	zw := gzip.NewWriter(w)
	_, err = tarFile.Seek(0, os.SEEK_SET)
	if err != nil {
		log.Fatal(err)
	}
	_, err = io.Copy(zw, tarFile)
	if err != nil {
		log.Fatal(err)
	}
	err = zw.Close()
	if err != nil {
		log.Fatal(err)
	}
	err = tarFile.Close()
	if err != nil {
		log.Fatal(err)
	}
	err = os.Remove(tarFile.Name())
	if err != nil {
		log.Fatal(err)
	}
}

func archiveZip(goos, arch string, m *manifest) {
	zipPath := fmt.Sprintf("decred-%s-%s-%s", goos, arch, relver)
	zipFile, err := os.Create(fmt.Sprintf("archive/%s.zip", zipPath))
	defer zipFile.Close()
	if err != nil {
		log.Fatal(err)
	}
	hash := sha256.New()
	w := io.MultiWriter(zipFile, hash)
	defer func() {
		name := filepath.Base(zipFile.Name())
		var sum [32]byte
		copy(sum[:], hash.Sum(nil))
		*m = append(*m, manifestLine{name, sum})
	}()
	log.Printf("archive: %v", zipFile.Name())
	zw := zip.NewWriter(w)
	addFile := func(name string, r io.Reader) {
		h := &zip.FileHeader{
			Name:   strings.ReplaceAll(filepath.Join(zipPath, name), `\`, `/`),
			Method: zip.Deflate,
		}
		f, err := zw.CreateHeader(h)
		if err != nil {
			log.Fatal(err)
		}
		_, err = io.Copy(f, r)
		if err != nil {
			log.Fatal(err)
		}
	}
	for i := range tools {
		exe := exeName(tools[i].builddir, goos)
		exePath := filepath.Join("bin", goos+"-"+arch, exe)
		exeFi, err := os.Open(exePath)
		if err != nil {
			log.Fatal(err)
		}
		addFile(exe, exeFi)
		exeFi.Close()
	}
	// for _, a := range assets {
	// 	addFile(a.name, bytes.NewBuffer(a.contents))
	// }
	err = zw.Close()
	if err != nil {
		log.Fatal(err)
	}
}

func writeManifest(m manifest) {
	fi, err := os.Create(fmt.Sprintf("archive/decred-%s-manifest.txt", relver))
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("manifest: %v", fi.Name())
	buf := new(bytes.Buffer)
	for i := range m {
		_, err = fmt.Fprintf(buf, "%x  %s\n", m[i].hash, m[i].name)
		if err != nil {
			log.Fatal(err)
		}
	}
	fp := sha256.Sum256(buf.Bytes())
	log.Printf("manifest hash: SHA256:%x", fp)
	_, err = io.Copy(fi, buf)
	if err != nil {
		log.Fatal(err)
	}
	err = fi.Close()
	if err != nil {
		log.Fatal(err)
	}
}
