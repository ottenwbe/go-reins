package licenses

import (
	"os"
	"path/filepath"
	"strings"
)

// moduleCache locates the local module cache: $GOMODCACHE, else the
// first entry of $GOPATH, else the default ~/go.
func moduleCache() (string, error) {
	if c := os.Getenv("GOMODCACHE"); c != "" {
		return c, nil
	}
	if p := os.Getenv("GOPATH"); p != "" {
		first := filepath.SplitList(p)[0]
		return filepath.Join(first, "pkg", "mod"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "go", "pkg", "mod"), nil
}

// cachePath encodes a module path the way the module cache does:
// every uppercase letter becomes a bang plus its lowercase form.
func cachePath(path string) string {
	var b strings.Builder
	for _, r := range path {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('!')
			b.WriteRune(r - 'A' + 'a')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// licenseFilePrefixes lists the base names a license file may carry,
// matched case-insensitively with any extension ("LICENSE",
// "COPYING", "LICENSE.md", ...).
var licenseFilePrefixes = []string{"LICENSE", "LICENCE", "COPYING", "COPYRIGHT"}

// maxSample bounds how much of a license file is read: the signature
// phrases live at the top of every known form.
const maxSample = 64 * 1024

// licenseText finds and reads the first license file in a module
// directory. ok is false when the directory is unreadable or holds
// no recognizable license file.
func licenseText(dir string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.ToUpper(e.Name())
		base := strings.TrimSuffix(name, filepath.Ext(name))
		if !matchesAny(base, licenseFilePrefixes) {
			continue
		}
		text, err := readHead(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		return text, true
	}
	return "", false
}

// matchesAny reports whether name equals one of the prefixes or
// starts with one followed by a separator, so "LICENSE-MIT" and
// "COPYING.LESSER" also qualify.
func matchesAny(name string, prefixes []string) bool {
	for _, p := range prefixes {
		if name == p || strings.HasPrefix(name, p+"-") || strings.HasPrefix(name, p+"_") {
			return true
		}
	}
	return false
}

func readHead(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, maxSample)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return "", err
	}
	return string(buf[:n]), nil
}
