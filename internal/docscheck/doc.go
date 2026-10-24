// Package docscheck holds repository checks for the documentation rules of
// docs/decisions.md (ADR-0017): every referenced Markdown file exists, and
// every story ID cited in code is defined in docs/requirements/stories.md.
// The checks are tests, so `go test ./...` runs them in CI on every push.
package docscheck
