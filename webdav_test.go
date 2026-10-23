package webdav

import (
	"testing"
)

func TestConditionalMatch(t *testing.T) {
	// testing match and no match
	val := ConditionalMatch("\"AAA\", \"BBB\", \"CCC\"")
	if isSet, ok, err := val.MatchETag("AAA"); err != nil {
		t.Fatal(err)
	} else if !isSet {
		t.Fatalf("Expected isSet true")
	} else if !ok {
		t.Fatalf("Expected ok true")
	}
	if isSet, ok, err := val.MatchETag("DDD"); err != nil {
		t.Fatal(err)
	} else if !isSet {
		t.Fatalf("Expected isSet true")
	} else if ok {
		t.Fatalf("Expected ok false")
	}

	// testing parse error
	val = ConditionalMatch("\"AAA\", \"BBB, CCC")
	if _, _, err := val.MatchETag("BBB"); err == nil {
		t.Fatalf("Expected non-nil error")
	}

	// testing WildCard
	val = ConditionalMatch("*")
	if isSet, ok, err := val.MatchETag("BBB"); err != nil {
		t.Fatal(err)
	} else if !isSet {
		t.Fatalf("Expected isSet true")
	} else if !ok {
		t.Fatalf("Expected ok true")
	}

	// testing isSet
	val = ConditionalMatch("")
	if isSet, _, _ := val.MatchETag("BBB"); isSet {
		t.Fatalf("Expected isSet false")
	}
}

func TestConditionalMatchWeak(t *testing.T) {
	val := ConditionalMatch(`W/"AAA", "BBB"`)
	for _, tc := range []struct {
		etag         string
		strong, weak bool
	}{
		{"AAA", false, true},
		{"BBB", true, true},
		{"CCC", false, false},
	} {
		if _, ok, err := val.MatchETag(tc.etag); err != nil {
			t.Fatal(err)
		} else if ok != tc.strong {
			t.Errorf("MatchETag(%q) = %v, want %v", tc.etag, ok, tc.strong)
		}
		if _, ok, err := val.MatchETagWeak(tc.etag); err != nil {
			t.Fatal(err)
		} else if ok != tc.weak {
			t.Errorf("MatchETagWeak(%q) = %v, want %v", tc.etag, ok, tc.weak)
		}
	}
}

func TestConditionalMatchEntityTag(t *testing.T) {
	val := ConditionalMatch(`"AAA"`)
	for _, tc := range []struct {
		etag         string
		strong, weak bool
	}{
		{`"AAA"`, true, true},
		{`W/"AAA"`, false, true},
		{`"BBB"`, false, false},
	} {
		if _, ok, err := val.MatchETag(tc.etag); err != nil {
			t.Fatal(err)
		} else if ok != tc.strong {
			t.Errorf("MatchETag(%q) = %v, want %v", tc.etag, ok, tc.strong)
		}
		if _, ok, err := val.MatchETagWeak(tc.etag); err != nil {
			t.Fatal(err)
		} else if ok != tc.weak {
			t.Errorf("MatchETagWeak(%q) = %v, want %v", tc.etag, ok, tc.weak)
		}
	}
}

func TestConditionalMatchOpaqueTags(t *testing.T) {
	for _, tc := range []struct {
		name, header, etag string
		strong, weak       bool
	}{
		{"comma", `"a,b"`, "a,b", true, true},
		{"comma in list", `"other", "a,b", "last"`, "a,b", true, true},
		{"comma mismatch", `"a,b"`, "a", false, false},
		{"weak comma", `W/"a,b"`, "a,b", false, true},
		{"weak comma in list", `"other", W/"a,b"`, "a,b", false, true},
		{"weak comma resource", `"a,b"`, `W/"a,b"`, false, true},
		{"literal backslash", `"a\b"`, `a\b`, true, true},
		{"backslash is not escaping", `"a\x62"`, "ab", false, false},
		{"backslash before closing quote", `"a\", "other"`, `a\`, true, true},
		{"quoted backslash resource", `W/"a\b"`, `"a\b"`, false, true},
		{"distinct literal backslashes", `"a\\b"`, `a\b`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			val := ConditionalMatch(tc.header)
			if set, match, err := val.MatchETag(tc.etag); err != nil || !set || match != tc.strong {
				t.Errorf("strong match = (%v, %v, %v), want (true, %v, nil)", set, match, err, tc.strong)
			}
			if set, match, err := val.MatchETagWeak(tc.etag); err != nil || !set || match != tc.weak {
				t.Errorf("weak match = (%v, %v, %v), want (true, %v, nil)", set, match, err, tc.weak)
			}
		})
	}
}

func TestConditionalMatchMalformedTags(t *testing.T) {
	for _, header := range []string{`"a,b`, `"other", "a,b`, `"a"junk`, `"a\"b"`, "\"a\tb\"", "\"a\x7fb\"", `W/"a,b`, `"a", "unterminated`} {
		t.Run(header, func(t *testing.T) {
			val := ConditionalMatch(header)
			for _, match := range []func(string) (bool, bool, error){val.MatchETag, val.MatchETagWeak} {
				if set, ok, err := match("a"); !set || ok || err == nil {
					t.Errorf("match = (%v, %v, %v), want (true, false, error)", set, ok, err)
				}
			}
		})
	}
}
