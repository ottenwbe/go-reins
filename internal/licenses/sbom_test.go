package licenses

import (
	"strings"
	"testing"
)

// TestSBOMRoundtrip pins the bake-in pipeline: entries in, CycloneDX
// out, parse back, same entries out.
func TestSBOMRoundtrip(t *testing.T) {
	entries := []Entry{
		{Module: "github.com/spf13/cobra", Version: "v1.10.2", License: "Apache-2.0"},
		{Module: "go.yaml.in/yaml/v3", Version: "v3.0.5", License: "Apache-2.0 / MIT"},
		{Module: "example.com/obscure", Version: "v0.1.0", License: "unknown"},
	}

	data, err := WriteCycloneDX("go-reins", entries)
	if err != nil {
		t.Fatalf("WriteCycloneDX: %v", err)
	}
	doc := string(data)
	for _, want := range []string{
		`"bomFormat": "CycloneDX"`,
		`"specVersion": "1.7"`,
		`"purl": "pkg:golang/github.com/spf13/cobra@v1.10.2"`,
		`"expression": "Apache-2.0 OR MIT"`,
		`"expression": "NOASSERTION"`,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("sbom missing %s:\n%s", want, doc)
		}
	}

	back, err := parseSBOM(data)
	if err != nil {
		t.Fatalf("parseSBOM: %v", err)
	}
	if len(back) != 3 {
		t.Fatalf("parsed %d entries, want 3", len(back))
	}
	// parseSBOM sorts by module; compare against the sorted order.
	sorted := []Entry{entries[2], entries[0], entries[1]}
	for i := range sorted {
		if back[i] != sorted[i] {
			t.Errorf("entry %d = %+v, want %+v", i, back[i], sorted[i])
		}
	}
}

// TestParseSBOMAcceptsBareIDs verifies the reader copes with the
// other common CycloneDX license form (bare ids, no expression).
func TestParseSBOMAcceptsBareIDs(t *testing.T) {
	doc := `{
  "bomFormat": "CycloneDX",
  "specVersion": "1.6",
  "metadata": {"component": {"type": "application", "name": "go-reins"}},
  "components": [
    {"type": "library", "name": "example.com/a", "version": "v1.0.0",
     "licenses": [{"license": {"id": "MIT"}}]}
  ]
}`
	entries, err := parseSBOM([]byte(doc))
	if err != nil {
		t.Fatalf("parseSBOM: %v", err)
	}
	if len(entries) != 1 || entries[0].License != "MIT" {
		t.Errorf("entries = %+v, want one MIT entry", entries)
	}
}

func TestParseSBOMRejectsWrongFormat(t *testing.T) {
	if _, err := parseSBOM([]byte(`{"bomFormat": "SPDX", "components": []}`)); err == nil {
		t.Error("want error for non-CycloneDX document")
	}
}

// TestListReadsEmbeddedSBOM asserts the committed document parses and
// reports the real dependency set.
func TestListReadsEmbeddedSBOM(t *testing.T) {
	entries, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("embedded sbom reports no dependencies")
	}
	for i := 1; i < len(entries); i++ {
		if entries[i-1].Module > entries[i].Module {
			t.Fatalf("entries not sorted: %q > %q", entries[i-1].Module, entries[i].Module)
		}
	}
}
