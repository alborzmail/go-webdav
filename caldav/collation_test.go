package caldav

import (
	"reflect"
	"testing"
)

func TestMatchTextMatchCollation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		txt   TextMatch
		want  bool
	}{
		{"default folds ascii", "Team Meeting", TextMatch{Text: "meeting"}, true},
		{"default leaves non-ascii", "Ärger", TextMatch{Text: "ärger"}, false},
		{"ascii-casemap", "Team Meeting", TextMatch{Text: "TEAM", Collation: CollationASCIICasemap}, true},
		{"unicode-casemap", "Ärger", TextMatch{Text: "ärger", Collation: CollationUnicodeCasemap}, true},
		{"octet is exact", "Team Meeting", TextMatch{Text: "meeting", Collation: CollationOctet}, false},
		{"octet contains", "Team Meeting", TextMatch{Text: "Meeting", Collation: CollationOctet}, true},
		{"negate", "Team Meeting", TextMatch{Text: "meeting", NegateCondition: true}, false},
		{"negate octet", "Team Meeting", TextMatch{Text: "meeting", Collation: CollationOctet, NegateCondition: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := matchTextMatch(tc.txt, tc.value)
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
	if _, err := matchTextMatch(TextMatch{Text: "a", Collation: "i;basic"}, "a"); err == nil {
		t.Error("matched with an unknown collation")
	}
}

func TestDecodeTextMatch(t *testing.T) {
	txt, err := decodeTextMatch(&textMatch{Text: "a", Collation: CollationOctet, NegateCondition: true})
	if err != nil {
		t.Fatal(err)
	}
	if *txt != (TextMatch{Text: "a", Collation: CollationOctet, NegateCondition: true}) {
		t.Errorf("decodeTextMatch = %+v", *txt)
	}

	_, err = decodeTextMatch(&textMatch{Text: "a", Collation: "i;basic"})
	if want := NewPreconditionError(PreconditionSupportedCollation); !reflect.DeepEqual(err, want) {
		t.Errorf("decodeTextMatch error = %v, want %v", err, want)
	}
}
