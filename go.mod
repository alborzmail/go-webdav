module github.com/emersion/go-webdav

go 1.27.1

require (
	github.com/alborzmail/go-recur v0.0.0-20260929133629-dcb73b0bbb94
	github.com/emersion/go-ical v0.0.0-20250609112844-439c63cef608
	github.com/emersion/go-vcard v0.0.0-20241024213814-c9703dde27ff
	golang.org/x/text v0.42.0
)

// Our fork of go-ical reads RSCALE rules and a component with its
// overrides as one series, which the time-range filter relies on.
replace github.com/emersion/go-ical => github.com/alborzmail/go-ical v0.0.0-20260929134259-007cc45012fd
