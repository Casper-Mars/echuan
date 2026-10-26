// Package material handles material (attachment) storage: content-addressed
// objects on disk plus the versioned materials rows that point at them.
//
// Layout under data_dir:
//
//	tmp/<random>              scratch file of an in-flight write
//	objects/<sha[:2]>/<sha>   content-addressed object, immutable once renamed
//
// A write streams the body into a scratch file while hashing it (so an
// oversized body is rejected early), fsyncs, and renames the file into place.
// Because the object name is the digest, re-submitting identical content
// reuses the existing object, yet a new materials row is still inserted so
// "who submitted what, when" is never lost.
//
// The row insert runs on the caller's write transaction: file first, row
// second (tech design §5.8). When the surrounding transaction fails the caller
// discards the object, but only when this write created it.
//
// Task-04 implements the write/read kernel used by the project requirement
// endpoints. Task-05 adds the download endpoints and the HTTP 413 mapping.
package material
