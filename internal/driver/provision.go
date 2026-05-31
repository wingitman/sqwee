package driver

import "context"

// ─── Provisioning (optional capability) ─────────────────────────────────────
//
// A Driver MAY also implement Provisioner to let sqwee create a brand-new
// database for that engine — a SQLite file, a CREATE DATABASE on a running
// server, or a freshly spun-up Docker container. The capability is discovered
// at runtime via a type assertion (driver.ByName(n).(Provisioner)), so the
// required Driver/Conn surface stays small and third-party drivers opt in.
//
// See SPEC.md for the full contract and per-driver notes.

// ProvisionField describes one input the wizard collects for provisioning.
type ProvisionField struct {
	// Key identifies the value in the values map passed to Provision
	// ("host", "port", "db_name", "password", ...).
	Key string
	// Label is the human prompt shown in the wizard.
	Label string
	// Default pre-fills the field.
	Default string
	// Placeholder is shown when the field is empty.
	Placeholder string
	// Options, when non-empty, makes this a selector field.
	Options []string
	// Password masks the input and marks it as a secret that must not be
	// persisted to disk.
	Password bool
	// Optional allows the field to be left blank.
	Optional bool
}

// ProvisionMode is a single strategy a driver offers for creating a database
// (e.g. a local file, CREATE DATABASE on a server, or a Docker container).
type ProvisionMode struct {
	// ID is the stable identifier passed back to Provision ("file", "server",
	// "docker").
	ID string
	// Label is the human description shown in the wizard.
	Label string
	// Fields are the inputs to collect for this mode.
	Fields []ProvisionField
}

// ProvisionResult is returned by Provision after a database has been created.
type ProvisionResult struct {
	// Info is a connection to the newly-created database. The Password field is
	// left empty; the UI maps a password env-var name onto the saved
	// connection (matching sqwee's existing password posture).
	Info ConnInfo
	// Steps is a human-readable log of what was done, shown in the summary.
	Steps []string
	// Container is the name of a Docker container that was started, or "" if
	// none. Recorded on the saved connection for reference.
	Container string
	// PasswordHint is a suggested env-var name (or the literal generated
	// password for Docker, shown once in the summary) — never persisted as-is.
	PasswordHint string
}

// Provisioner is implemented by drivers that can create a new database.
type Provisioner interface {
	// ProvisionModes returns the strategies this driver offers (at least one).
	ProvisionModes() []ProvisionMode
	// Provision creates the database for the chosen mode using the collected
	// field values, and returns a connection to it.
	Provision(ctx context.Context, mode string, values map[string]string) (ProvisionResult, error)
}

// Provisioners returns the names of registered drivers that support
// provisioning, sorted (via All()).
func Provisioners() []string {
	var names []string
	for _, d := range All() {
		if _, ok := d.(Provisioner); ok {
			names = append(names, d.Name())
		}
	}
	return names
}

// AsProvisioner returns the named driver as a Provisioner, or nil if it does
// not support provisioning.
func AsProvisioner(name string) Provisioner {
	d := ByName(name)
	if d == nil {
		return nil
	}
	if p, ok := d.(Provisioner); ok {
		return p
	}
	return nil
}
