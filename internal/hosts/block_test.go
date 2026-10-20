package hosts

import (
	"bytes"
	"strings"
	"testing"
)

var testMarkers = Markers{
	Begin: "# >>> ghaccel managed block (v1) >>>",
	End:   "# <<< ghaccel managed block (v1) <<<",
}

func TestSpliceAppendAndIdempotent(t *testing.T) {
	original := []byte("127.0.0.1 localhost\r\n::1 localhost\r\n")
	mappings := map[string]string{"github.com": "1.2.3.4", "api.github.com": "5.6.7.8"}

	first, skipped, err := Splice(original, mappings, testMarkers, "skip")
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Fatalf("unexpected skips: %v", skipped)
	}
	if !bytes.HasPrefix(first, original) {
		t.Fatal("original content was modified")
	}
	if !strings.Contains(string(first), "1.2.3.4 github.com\r\n") {
		t.Fatalf("block missing or wrong EOL:\n%s", first)
	}

	second, _, err := Splice(first, mappings, testMarkers, "skip")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("splice is not idempotent:\nfirst=%q\nsecond=%q", first, second)
	}
}

func TestSpliceReplacePreservesOutside(t *testing.T) {
	original := []byte("10.0.0.1 example.com\n" + testMarkers.Begin + "\n9.9.9.9 github.com\n" + testMarkers.End + "\n# tail\n")
	updated, _, err := Splice(original, map[string]string{"github.com": "1.1.1.1"}, testMarkers, "skip")
	if err != nil {
		t.Fatal(err)
	}
	s := string(updated)
	if !strings.HasPrefix(s, "10.0.0.1 example.com\n") || !strings.HasSuffix(s, "# tail\n") {
		t.Fatalf("outside content not preserved:\n%s", s)
	}
	if strings.Contains(s, "9.9.9.9") || !strings.Contains(s, "1.1.1.1 github.com") {
		t.Fatalf("block not replaced:\n%s", s)
	}
}

func TestSpliceBOMAndCRLF(t *testing.T) {
	original := append([]byte{0xEF, 0xBB, 0xBF}, []byte("127.0.0.1 localhost\r\n")...)
	updated, _, err := Splice(original, map[string]string{"github.com": "1.2.3.4"}, testMarkers, "skip")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(updated, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatal("BOM lost")
	}
	if !bytes.Contains(updated, []byte("1.2.3.4 github.com\r\n")) {
		t.Fatalf("expected CRLF block:\n%q", updated)
	}
}

func TestSpliceDuplicateMarkersRejected(t *testing.T) {
	original := []byte(testMarkers.Begin + "\n" + testMarkers.End + "\n" + testMarkers.Begin + "\n" + testMarkers.End + "\n")
	if _, _, err := Splice(original, map[string]string{"github.com": "1.1.1.1"}, testMarkers, "skip"); err == nil {
		t.Fatal("expected malformed block error")
	}
}

func TestSpliceOrphanMarkerRejected(t *testing.T) {
	original := []byte(testMarkers.Begin + "\n1.1.1.1 github.com\n")
	if _, _, err := Splice(original, map[string]string{"github.com": "2.2.2.2"}, testMarkers, "skip"); err == nil {
		t.Fatal("expected malformed block error")
	}
}

func TestSpliceUserConflictSkipped(t *testing.T) {
	original := []byte("1.2.3.4 github.com\n")
	updated, skipped, err := Splice(original, map[string]string{"github.com": "5.6.7.8"}, testMarkers, "skip")
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 1 || skipped[0] != "github.com" {
		t.Fatalf("expected github.com skipped, got %v", skipped)
	}
	if !bytes.Equal(updated, original) {
		t.Fatalf("user entry should not be touched:\n%s", updated)
	}
}

func TestRemoveRestoresOriginal(t *testing.T) {
	original := []byte("127.0.0.1 localhost\n")
	withBlock, _, err := Splice(original, map[string]string{"github.com": "1.2.3.4"}, testMarkers, "skip")
	if err != nil {
		t.Fatal(err)
	}
	restored, changed, err := Remove(withBlock, testMarkers)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected change")
	}
	if !bytes.Equal(restored, original) {
		t.Fatalf("restore mismatch:\nwant=%q\ngot =%q", original, restored)
	}
}

func TestMappings(t *testing.T) {
	original := []byte("127.0.0.1 localhost\n" + testMarkers.Begin + "\n1.2.3.4 github.com\n5.6.7.8 api.github.com\n" + testMarkers.End + "\n")
	m, present, err := Mappings(original, testMarkers)
	if err != nil {
		t.Fatal(err)
	}
	if !present || len(m) != 2 || m["github.com"] != "1.2.3.4" {
		t.Fatalf("unexpected mappings: %v present=%v", m, present)
	}
}

func TestSpliceNoTrailingNewline(t *testing.T) {
	original := []byte("127.0.0.1 localhost")
	updated, _, err := Splice(original, map[string]string{"github.com": "1.2.3.4"}, testMarkers, "skip")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(updated, original) {
		t.Fatal("prefix changed")
	}
	if !strings.Contains(string(updated), "\n1.2.3.4 github.com") {
		t.Fatalf("block not separated:\n%q", updated)
	}
}
