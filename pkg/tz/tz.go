// Package tz validates and loads user IANA time zones shared across services.
package tz

import (
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // slim images ship no zoneinfo
)

// Default is used when a user has no (valid) zone.
const Default = "UTC"

// zones caches valid names only, so hostile input can't grow it past the ~600 IANA names.
var zones sync.Map // string -> *time.Location

// lookup resolves an exact IANA name; zoneinfo files also hold non-locations like "Factory", so
// only "UTC"/"GMT" and Area/City names pass.
func lookup(name string) (*time.Location, bool) {
	if v, ok := zones.Load(name); ok {
		return v.(*time.Location), true
	}
	if len(name) > 64 || (name != "UTC" && name != "GMT" && !strings.Contains(name, "/")) {
		return nil, false
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, false
	}
	zones.Store(name, loc)
	return loc, true
}

// Valid reports whether name is a loadable IANA zone.
func Valid(name string) bool {
	_, ok := lookup(name)
	return ok
}

// Normalize returns name when valid, else Default.
func Normalize(name string) string {
	name = strings.TrimSpace(name)
	if !Valid(name) {
		return Default
	}
	return name
}

// Load returns the zone for name, falling back to UTC.
func Load(name string) *time.Location {
	if loc, ok := lookup(strings.TrimSpace(name)); ok {
		return loc
	}
	return time.UTC
}
