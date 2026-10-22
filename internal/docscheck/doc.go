// Package docscheck holds repository checks for the documentation rules of
// docs/adr/0017-documentation-structure-and-id-scheme.md: every referenced
// Markdown file exists, and retired document IDs do not come back. The checks
// are tests, so `go test ./...` runs them in CI on every push.
package docscheck
