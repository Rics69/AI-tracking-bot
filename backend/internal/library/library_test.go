package library

import (
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.List()) < 3 {
		t.Fatalf("guides = %d", len(c.List()))
	}
	for _, g := range c.List() {
		if strings.Contains(g.Preview, "\n\n") || g.Preview == g.Body {
			t.Errorf("%s: the preview must be only the first paragraph", g.Slug)
		}
	}
	if _, err := c.Get("nothing"); err == nil {
		t.Error("an unknown guide was found")
	}
}

func TestParseChecksTheHeader(t *testing.T) {
	for name, data := range map[string]string{
		"no header":  "текст",
		"not closed": "---\ntitle: a\n",
		"no title":   "---\ntopic: a\n---\nтекст",
		"no body":    "---\ntitle: a\n---\n",
	} {
		if _, err := parse("a", []byte(data)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
