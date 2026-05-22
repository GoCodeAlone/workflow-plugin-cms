package postgres

import "testing"

func TestParseThemeID(t *testing.T) {
	got, err := parseThemeID("")
	if err != nil {
		t.Fatalf("empty theme id: %v", err)
	}
	if got.Valid {
		t.Fatalf("empty theme id Valid = true, want false")
	}

	got, err = parseThemeID("42")
	if err != nil {
		t.Fatalf("numeric theme id: %v", err)
	}
	if !got.Valid || got.Int64 != 42 {
		t.Fatalf("numeric theme id = %#v, want 42", got)
	}

	if _, err := parseThemeID("default"); err == nil {
		t.Fatal("non-numeric theme id should fail until theme CRUD lands")
	}
}

func TestNormalizeHost(t *testing.T) {
	got := normalizeHost("WWW.Example.COM:443")
	if got != "www.example.com" {
		t.Fatalf("normalizeHost = %q, want www.example.com", got)
	}
}
