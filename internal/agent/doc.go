// Package agent wires kondukter-agent together (SPEC §7): the local store,
// the driver, and the goroutines that stream, reconcile, report inventory,
// meter and manage access. Each concern lives in a subpackage.
package agent
