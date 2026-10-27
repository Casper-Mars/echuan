// Package inbox implements the human inbox: pending reviews, task decisions
// and how long they have been waiting.
//
// The package is pure derivation (see inbox.go). Reading the rows is owned by
// internal/store, clock/configuration wiring by internal/app and the wire
// contract by internal/api.
package inbox
