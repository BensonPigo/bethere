package docscheck

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A released migration keeps its comments as they were (ADR-0019).
var exempt = []string{
	"internal/store/migrations/0001_init.sql",
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

// Placeholders in this file's own comments, not references.
var placeholders = map[string]bool{"name.md": true}

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

// storyID matches a story or acceptance-criterion ID in code, written either
// as in the docs ("US-M-3.6", "US-X-8.C1") or as in test names ("US_M_3_6").
var storyID = regexp.MustCompile(`US[-_](M|ST|H|X|VL|RN|RC|CA|O)[-_]([0-9]+)(?:[._-](C?[0-9]+))?`)

// TestStoryIDsExist checks that every story ID cited in code is defined in
// docs/requirements/stories.md, so a test never points at a removed criterion.
func TestStoryIDsExist(t *testing.T) {
	root, files := repoFiles(t)
	stories := readFile(t, root, "docs/requirements/stories.md")
	for _, f := range files {
		if strings.HasSuffix(f, ".md") {
			continue
		}
		text := readFile(t, root, f)
		for _, m := range storyID.FindAllStringSubmatchIndex(text, -1) {
			id := "US-" + text[m[2]:m[3]] + "-" + text[m[4]:m[5]]
			if m[6] >= 0 {
				id += "." + text[m[6]:m[7]]
			}
			if !regexp.MustCompile(regexp.QuoteMeta(id) + `([^0-9]|$)`).MatchString(stories) {
				t.Errorf("%s:%d: %s is not in docs/requirements/stories.md", f, lineOf(text, m[0]), id)
			}
		}
	}
}
