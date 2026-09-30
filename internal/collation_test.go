package internal

import "testing"

func TestCollate(t *testing.T) {
	for _, tc := range []struct {
		collation string
		a, b      string
		equal     bool
	}{
		{CollationOctet, "abc", "abc", true},
		{CollationOctet, "abc", "ABC", false},
		{CollationASCIICasemap, "abc", "ABC", true},
		{CollationASCIICasemap, "Ärger", "äRGER", false},
		{CollationASCIICasemap, "Ärger", "ÄRGER", true},
		{CollationUnicodeCasemap, "Ärger", "ärger", true},
		{CollationUnicodeCasemap, "Ärger", "ÄRGER", true},
		{CollationUnicodeCasemap, "ǆ", "ǅ", true},
		{CollationUnicodeCasemap, "Ärger", "Arger", false},
	} {
		collate, ok := Collate(tc.collation)
		if !ok {
			t.Fatalf("Collate(%q) not known", tc.collation)
		}
		if got := collate(tc.a) == collate(tc.b); got != tc.equal {
			t.Errorf("%s: %q == %q is %v, want %v", tc.collation, tc.a, tc.b, got, tc.equal)
		}
	}
	if _, ok := Collate("i;basic"); ok {
		t.Errorf("Collate(%q) known", "i;basic")
	}
}
