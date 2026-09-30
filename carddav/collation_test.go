package carddav

import (
	"reflect"
	"testing"

	"github.com/emersion/go-vcard"
)

func TestMatchTextMatchCollation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		txt   TextMatch
		want  bool
	}{
		{"default folds unicode", "Ärger", TextMatch{Text: "ärger"}, true},
		{"default contains", "Herr Ärger", TextMatch{Text: "ÄRG"}, true},
		{"unicode-casemap equals", "Ärger", TextMatch{Text: "ÄRGER", MatchType: MatchEquals, Collation: CollationUnicodeCasemap}, true},
		{"unicode-casemap equals is whole", "Ärger", TextMatch{Text: "ärg", MatchType: MatchEquals}, false},
		{"unicode-casemap starts-with", "Ärger", TextMatch{Text: "äR", MatchType: MatchStartsWith}, true},
		{"unicode-casemap ends-with", "Ärger", TextMatch{Text: "GER", MatchType: MatchEndsWith}, true},
		{"unicode-casemap starts-with misses", "Ärger", TextMatch{Text: "ger", MatchType: MatchStartsWith}, false},
		{"ascii-casemap folds ascii", "Alice", TextMatch{Text: "aLICE", MatchType: MatchEquals, Collation: CollationASCIICasemap}, true},
		{"ascii-casemap leaves non-ascii", "Ärger", TextMatch{Text: "ärger", MatchType: MatchEquals, Collation: CollationASCIICasemap}, false},
		{"octet equals", "Alice", TextMatch{Text: "Alice", MatchType: MatchEquals, Collation: CollationOctet}, true},
		{"octet is exact", "Alice", TextMatch{Text: "alice", Collation: CollationOctet}, false},
		{"negate", "Ärger", TextMatch{Text: "ärger", NegateCondition: true}, false},
		{"negate octet", "Alice", TextMatch{Text: "alice", Collation: CollationOctet, NegateCondition: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := matchTextMatch(tc.txt, &vcard.Field{Value: tc.value})
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("matchTextMatch(%+v, %q) = %v, want %v", tc.txt, tc.value, got, tc.want)
			}
		})
	}
}

func TestMatchTextMatchUnknownCollation(t *testing.T) {
	if _, err := matchTextMatch(TextMatch{Text: "a", Collation: "i;basic"}, &vcard.Field{Value: "a"}); err == nil {
		t.Error("matched with an unknown collation")
	}
}

func TestDecodeTextMatchUnknownCollation(t *testing.T) {
	_, err := decodePropFilter(&propFilter{Name: vcard.FieldFormattedName, TextMatches: []textMatch{{Text: "a", Collation: "i;basic"}}})
	if want := NewPreconditionError(PreconditionSupportedCollation); !reflect.DeepEqual(err, want) {
		t.Errorf("decodePropFilter error = %v, want %v", err, want)
	}
}
