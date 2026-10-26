// Package baresql holds what the BareSQL commands share: the release version.
// BareSQL Audit lives in package audit, BareSQL Portable in package portable.
package baresql

// Version is the BareSQL release. VERSION and CHANGELOG.md must agree with it
// (scripts/check-version.sh). A version ending in -dev is never released.
const Version = "0.1.0"
