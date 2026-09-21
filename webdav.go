// Package webdav provides a client and server WebDAV filesystem implementation.
//
// WebDAV is defined in RFC 4918.
package webdav

import (
	"encoding/xml"
	"strings"
	"time"

	"github.com/emersion/go-webdav/internal"
)

// FileInfo holds information about a WebDAV file.
type FileInfo struct {
	Path     string
	Size     int64
	ModTime  time.Time
	IsDir    bool
	MIMEType string
	ETag     string
}

type CreateOptions struct {
	IfMatch     ConditionalMatch
	IfNoneMatch ConditionalMatch
}

type RemoveAllOptions struct {
	IfMatch     ConditionalMatch
	IfNoneMatch ConditionalMatch
}

type CopyOptions struct {
	NoRecursive bool
	NoOverwrite bool
}

// DeadProperty is a property the server stores for its clients without
// knowing what it means (RFC 4918 section 4.1). XML is the property's element.
type DeadProperty struct {
	Name xml.Name
	XML  []byte
}

type MoveOptions struct {
	NoOverwrite bool
}

// ConditionalMatch represents the value of a conditional header
// according to RFC 2068 section 14.25 and RFC 2068 section 14.26
// The (optional) value can either be a wildcard or an ETag.
type ConditionalMatch string

func (val ConditionalMatch) IsSet() bool {
	return val != ""
}

func (val ConditionalMatch) IsWildcard() bool {
	return val == "*"
}

func (val ConditionalMatch) ETag() (string, error) {
	var e internal.ETag
	if err := e.UnmarshalText([]byte(val)); err != nil {
		return "", err
	}
	return string(e), nil
}

// MatchETag checks if the ETag provided matches any of the ETags in the ConditionalMatch header value.
//
// Parameters:
//   - etag: The ETag to match against.
//
// Returns:
//   - isSet: Indicates if the ConditionalMatch has any ETags set, or is wildcard.
//   - match: True if the etag matches any of the ETags in ConditionalMatch, false otherwise.
//   - err: An error if there was a problem parsing one of the ETags.
//     If an error occurs during parsing, match will be set to false, but isSet will be true.
//     Callers should check for a non-nil error to ensure the match result is valid.
//
// The function returns early if no ETags are set (isSet is false) or if a wildcard (*) is used,
// in which case all ETags match. For multiple ETags, it checks each one until a match is found or all are checked.
//
// The comparison is the strong one If-Match asks for: a weak ETag in the
// header value never matches (RFC 7232 section 2.3.2).
func (val ConditionalMatch) MatchETag(etag string) (isSet bool, match bool, err error) {
	return val.matchETag(etag, false)
}

// MatchETagWeak is like MatchETag with the weak comparison If-None-Match asks
// for: a weak ETag in the header value matches the ETag it was made from.
func (val ConditionalMatch) MatchETagWeak(etag string) (isSet bool, match bool, err error) {
	return val.matchETag(etag, true)
}

func (val ConditionalMatch) matchETag(etag string, weak bool) (isSet bool, match bool, err error) {
	if !val.IsSet() {
		return false, false, nil
	} else if etag == "" {
		return true, false, nil
	} else if val.IsWildcard() {
		return true, true, nil
	}
	quoted_etags := strings.Split(string(val), ",")
	for _, quoted_etag := range quoted_etags {
		quoted_etag = strings.TrimSpace(quoted_etag)
		if strings.HasPrefix(quoted_etag, "W/") {
			if !weak {
				continue
			}
			quoted_etag = strings.TrimPrefix(quoted_etag, "W/")
		}
		var e internal.ETag
		if err := e.UnmarshalText([]byte(quoted_etag)); err != nil {
			// opinionated returning `false` on match so caller
			// should definitely check for non-nil `err`
			return true, false, err
		} else if string(e) == etag {
			return true, true, nil
		}
	}
	return true, false, nil
}
