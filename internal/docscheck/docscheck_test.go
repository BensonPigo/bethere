package docscheck

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Files that keep retired IDs or old paths on purpose (docs/glossary.md
// section 2): archived records (including the ID mapping), the ADR describing
// the change, and a released migration that must not be edited (ADR-0019).
var exempt = []string{
	"docs/archive/",
	"docs/adr/0017-documentation-structure-and-id-scheme.md",
	"internal/store/migrations/0001_init.sql",
	"internal/docscheck/docscheck_test.go", // names the retired patterns
}

var scanned = map[string]bool{
	".md": true, ".go": true, ".ts": true, ".tsx": true, ".css": true,
	".sql": true, ".sh": true, ".yml": true,
}

var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "dist": true, "build": true, ".claude": true,
}

// repoFiles returns the scanned files, as slash paths relative to the
// repository root, minus the exempt ones.
func repoFiles(t *testing.T) (root string, files []string) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repository root not found at %s: %v", root, err)
	}
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !scanned[filepath.Ext(rel)] || isExempt(rel) {
			return nil
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return root, files
}

func isExempt(rel string) bool {
	for _, e := range exempt {
		if rel == e || (strings.HasSuffix(e, "/") && strings.HasPrefix(rel, e)) {
			return true
		}
	}
	return false
}

func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// lineOf returns the 1-based line of byte offset i in s.
func lineOf(s string, i int) int { return strings.Count(s[:i], "\n") + 1 }

var mdRef = regexp.MustCompile(`[A-Za-z0-9_./-]+\.md\b`)

// Placeholders in the ADR format description, not references.
var placeholders = map[string]bool{"NNNN-short-title.md": true}

// TestMarkdownReferencesExist checks that every `name.md` mentioned in docs
// and code resolves. Docs cite each other by path from docs/ ("design/api.md")
// or by bare file name ("api.md"), so a reference resolves when it exists
// relative to the repository root, to docs/, or to the citing file, or when
// exactly one Markdown file under docs/ ends with it.
func TestMarkdownReferencesExist(t *testing.T) {
	root, files := repoFiles(t)
	var docs []string
	_ = filepath.WalkDir(filepath.Join(root, "docs"), func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".md") {
			rel, _ := filepath.Rel(root, path)
			docs = append(docs, filepath.ToSlash(rel))
		}
		return nil
	})
	exists := func(rel string) bool {
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
		return err == nil
	}
	for _, f := range files {
		text := readFile(t, root, f)
		for _, loc := range mdRef.FindAllStringIndex(text, -1) {
			ref := strings.TrimPrefix(text[loc[0]:loc[1]], "./")
			if placeholders[ref] || strings.Contains(ref, "://") {
				continue
			}
			if exists(ref) || exists("docs/"+ref) || exists(filepath.ToSlash(filepath.Join(filepath.Dir(f), ref))) {
				continue
			}
			n := 0
			for _, d := range docs {
				if strings.HasSuffix(d, "/"+ref) {
					n++
				}
			}
			if n == 1 {
				continue
			}
			t.Errorf("%s:%d: %q does not resolve to a file (%d matches under docs/)", f, lineOf(text, loc[0]), ref, n)
		}
	}
}

// Retired IDs (docs/archive/retired-ids.md). The kept IDs are not listed:
// milestones M0–M3, priorities P0–P2, principles V1–V5, Sev, NFR, RISK, ADR
// and story IDs such as US-M-1.C2. M1–M3 of the old data-model list collide
// with milestones and cannot be told apart, so they are not checked.
var retired = regexp.MustCompile(`(^|[^A-Za-z0-9_.\-])(` +
	`[ABCEFKQRSTU][0-9]{1,2}[a-d]?` + // clarification A–F, sync S, screen U, tech T/R, api A/F, overview Q/K
	`|T-[A-F]|D-[1-4]|D[12]` + // tech decision log, process decisions, scope questions
	`|M(?:[4-9]|10)|P[3-8]|V2a|V6` + // data-model M4–M10, access-control P3–P8, screens
	`)($|[^A-Za-z0-9_\-])`)

// allowed are phrases that look like retired IDs but are not.
var allowed = []string{"C4 模型"}

// archiveCite is a citation of an archived record with its own IDs, such as
// "`archive/clarification.md` B1、B4" (docs/glossary.md section 2).
var archiveCite = regexp.MustCompile("`archive/[^`]+\\.md`[ ]*(?:[A-Z][A-Za-z0-9-]*(?:[、～][A-Z]?[A-Za-z0-9-]*)*)")

func TestNoRetiredIDs(t *testing.T) {
	root, files := repoFiles(t)
	for _, f := range files {
		text := readFile(t, root, f)
		masked := text
		for _, a := range allowed {
			masked = strings.ReplaceAll(masked, a, strings.Repeat("_", len(a)))
		}
		masked = archiveCite.ReplaceAllStringFunc(masked, func(s string) string { return strings.Repeat("_", len(s)) })
		// Search again from the end of each ID rather than of the match, so
		// the separator in "T15/T18" can bound both.
		for pos := 0; pos < len(masked); {
			m := retired.FindStringSubmatchIndex(masked[pos:])
			if m == nil {
				break
			}
			start, end := pos+m[4], pos+m[5]
			t.Errorf("%s:%d: retired ID %q; see docs/archive/retired-ids.md for where it went",
				f, lineOf(text, start), text[start:end])
			pos = end
		}
	}
}
