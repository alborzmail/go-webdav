package internal

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Collations a text-match may name (RFC 4790 section 9, RFC 5051).
const (
	CollationOctet          = "i;octet"
	CollationASCIICasemap   = "i;ascii-casemap"
	CollationUnicodeCasemap = "i;unicode-casemap"
)

// Collate returns the canonical form collation compares strings in, and
// false for a collation not implemented here.
func Collate(collation string) (func(string) string, bool) {
	switch collation {
	case CollationOctet:
		return func(s string) string { return s }, true
	case CollationASCIICasemap:
		return foldASCII, true
	case CollationUnicodeCasemap:
		return foldUnicode, true
	}
	return nil, false
}

func foldASCII(s string) string {
	return strings.Map(func(r rune) rune {
		if 'a' <= r && r <= 'z' {
			return r - 'a' + 'A'
		}
		return r
	}, s)
}

// foldUnicode is RFC 5051 section 2: titlecase each character, then
// decompose to NFKD.
func foldUnicode(s string) string {
	return norm.NFKD.String(strings.Map(unicode.ToTitle, s))
}
