package driver

import (
	"sort"
	"strings"
)

// ─── Registry ───────────────────────────────────────────────────────────────
//
// Drivers self-register from an init() function via Register. main.go blank-
// imports this package so every built-in driver's init() runs at startup. The
// same mechanism works for third-party drivers added to this package.

var registry []Driver

// Register adds a driver to the registry. Call it from an init() function.
// Registering two drivers with the same Name() panics, since that is a
// programmer error that would make resolution ambiguous.
func Register(d Driver) {
	for _, existing := range registry {
		if existing.Name() == d.Name() {
			panic("driver: duplicate driver name " + d.Name())
		}
	}
	registry = append(registry, d)
}

// All returns a copy of the registered drivers, sorted by name.
func All() []Driver {
	out := make([]Driver, len(registry))
	copy(out, registry)
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Names returns the registered driver names, sorted.
func Names() []string {
	all := All()
	names := make([]string, len(all))
	for i, d := range all {
		names[i] = d.Name()
	}
	return names
}

// ByName returns the registered driver with the given name, or nil.
func ByName(name string) Driver {
	for _, d := range registry {
		if d.Name() == name {
			return d
		}
	}
	return nil
}

// ForScheme returns the registered driver that handles the given URL scheme
// (case-insensitive), or nil.
func ForScheme(scheme string) Driver {
	scheme = strings.ToLower(scheme)
	for _, d := range registry {
		for _, s := range d.Schemes() {
			if strings.EqualFold(s, scheme) {
				return d
			}
		}
	}
	return nil
}

// Resolve picks the driver for a ConnInfo: an explicit info.Driver wins,
// otherwise the scheme of info.URL is used. Returns nil if nothing matches.
func Resolve(info ConnInfo) Driver {
	if info.Driver != "" {
		if d := ByName(info.Driver); d != nil {
			return d
		}
	}
	if info.URL != "" {
		if i := strings.Index(info.URL, "://"); i > 0 {
			if d := ForScheme(info.URL[:i]); d != nil {
				return d
			}
		}
	}
	return nil
}
