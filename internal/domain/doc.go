// Package domain holds the pure domain model: entities, value types and state
// invariants. It must not perform I/O and only depends on the standard
// library.
//
// Task-03 adds the Product entity and the shared error types; Task-04 adds the
// Project entity, the material entity and the projects.status table; Task-06
// adds the project and task state machines plus the Decision value type.
// Tasks, attempts and inbox follow in later tasks.
package domain
