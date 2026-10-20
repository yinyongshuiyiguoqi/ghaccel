package domains

import "testing"

func TestEffectiveExcludes(t *testing.T) {
	got := Effective(nil, []string{"uploads.github.com"}, []string{"github.io"})
	foundUploads := false
	for _, d := range got {
		if d == "github.io" {
			t.Fatal("excluded domain still present")
		}
		if d == "uploads.github.com" {
			foundUploads = true
		}
	}
	if !foundUploads {
		t.Fatal("extra domain missing")
	}
}

func TestSeedsForRawFamily(t *testing.T) {
	seeds := SeedsFor("raw.githubusercontent.com")
	if len(seeds) != 4 {
		t.Fatalf("expected 4 seeds, got %v", seeds)
	}
	if len(SeedsFor("github.com")) == 0 {
		t.Fatal("github.com should have seeds")
	}
}

func TestRequired(t *testing.T) {
	if !IsRequired("github.com") {
		t.Fatal("github.com must be required")
	}
	if IsRequired("github.io") {
		t.Fatal("github.io must be optional")
	}
}
