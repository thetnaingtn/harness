// KBSkills implements runtime.SkillProvider on top of the embedded
// markdown KB articles in ./kb/. The runtime calls FormatIndex once at
// BuildRuntime time and inlines the result into the static (cacheable)
// system prompt; full bodies load on demand via the auto-registered
// load_skill tool, keyed by the slug (filename without extension).
package main

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

// kbFS is the bundled set of KB articles. Embedded so the example runs
// from anywhere — no need to chdir to the example directory.
//
//go:embed kb/*.md
var kbFS embed.FS

// KBSkills is a SkillProvider backed by kbFS. Articles are loaded once
// at construction; a real consumer would re-parse on a watcher signal.
type KBSkills struct {
	bySlug map[string]kbArticle
	slugs  []string // sorted, for stable index ordering
}

type kbArticle struct {
	slug    string
	summary string // first non-blank, non-heading line of the body
	body    string
}

// NewKBSkills loads every kb/*.md, parses out a one-line summary, and
// returns a ready-to-use SkillProvider. Panics on a malformed embed
// (build-time failure, not a runtime concern).
func NewKBSkills() *KBSkills {
	entries, err := fs.ReadDir(kbFS, "kb")
	if err != nil {
		panic(fmt.Sprintf("kb embed: %v", err))
	}
	k := &KBSkills{bySlug: make(map[string]kbArticle, len(entries))}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		raw, err := fs.ReadFile(kbFS, "kb/"+e.Name())
		if err != nil {
			panic(fmt.Sprintf("read kb/%s: %v", e.Name(), err))
		}
		slug := strings.TrimSuffix(e.Name(), ".md")
		body := string(raw)
		k.bySlug[slug] = kbArticle{
			slug:    slug,
			summary: extractSummary(body),
			body:    body,
		}
		k.slugs = append(k.slugs, slug)
	}
	sort.Strings(k.slugs)
	return k
}

// FormatIndex returns the cacheable index of articles. Kept compact —
// the index goes into every cached prompt, so we summarise rather than
// inline bodies.
func (k *KBSkills) FormatIndex() string {
	var b strings.Builder
	b.WriteString("## Knowledge Base\n\n")
	b.WriteString("Use load_skill(\"<slug>\") to read the full article.\n\n")
	for _, slug := range k.slugs {
		fmt.Fprintf(&b, "- **%s** — %s\n", slug, k.bySlug[slug].summary)
	}
	return b.String()
}

// Get implements SkillProvider. Returns the raw markdown body.
func (k *KBSkills) Get(name string) (string, bool) {
	a, ok := k.bySlug[name]
	if !ok {
		return "", false
	}
	return a.body, true
}

// Slugs returns the article slugs (sorted). Used by KBSearchTool.
func (k *KBSkills) Slugs() []string { return append([]string(nil), k.slugs...) }

// Article returns the (summary, body) for a slug. Used by KBSearchTool
// for snippet preview.
func (k *KBSkills) Article(slug string) (summary, body string, ok bool) {
	a, ok := k.bySlug[slug]
	if !ok {
		return "", "", false
	}
	return a.summary, a.body, true
}

// extractSummary pulls the first non-heading, non-blank line out of an
// article. Falls back to the slug-friendly title if nothing matches.
func extractSummary(body string) string {
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if len(t) > 160 {
			t = t[:160] + "..."
		}
		return t
	}
	return "(no summary)"
}
