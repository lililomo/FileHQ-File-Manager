// Package fsjail is the only place where user files are touched (SPEC §5).
//
// Every operation goes through an *os.Root opened on the user's home
// directory, so the kernel (openat2 with RESOLVE_BENEATH on Linux) rejects
// path traversal and symlink escapes, including races where a directory is
// swapped for a symlink while an operation is running. Callers never build
// absolute host paths themselves: they pass virtual paths (VPath, "/" is the
// user's root) and receive virtual paths back.
//
// The package is Linux-only (statx, renameat, copy_file_range); the product
// targets a Linux server (ADR-01).
package fsjail
