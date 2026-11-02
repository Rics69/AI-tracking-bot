// Package library holds the guides of the club: Markdown files in the
// repository with a header of metadata. A new guide is a commit.
package library

import (
	"bytes"
	"cmp"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed content/*.md
var files embed.FS

// ErrNotFound: no such guide.
var ErrNotFound = errors.New("guide not found")

// Guide is one material.
type Guide struct {
	Slug    string
	Title   string `yaml:"title"`
	Topic   string `yaml:"topic"`
	Level   int    `yaml:"level"`
	Preview string // the first paragraph: what everyone sees
	Body    string // Markdown: what the club sees
}

// Catalog is the guides in order of level.
type Catalog struct{ guides []Guide }

// Load reads and checks the embedded guides.
func Load() (Catalog, error) {
	entries, err := fs.ReadDir(files, "content")
	if err != nil {
		return Catalog{}, err
	}
	var c Catalog
	for _, e := range entries {
		data, err := files.ReadFile("content/" + e.Name())
		if err != nil {
			return Catalog{}, err
		}
		g, err := parse(strings.TrimSuffix(e.Name(), ".md"), data)
		if err != nil {
			return Catalog{}, fmt.Errorf("%s: %w", e.Name(), err)
		}
		c.guides = append(c.guides, g)
	}
	slices.SortStableFunc(c.guides, func(a, b Guide) int { return cmp.Compare(a.Level, b.Level) })
	return c, nil
}

var fence = []byte("---\n")

func parse(slug string, data []byte) (Guide, error) {
	if !bytes.HasPrefix(data, fence) {
		return Guide{}, errors.New("no header")
	}
	head, body, ok := bytes.Cut(data[len(fence):], fence)
	if !ok {
		return Guide{}, errors.New("the header is not closed")
	}
	g := Guide{Slug: slug}
	if err := yaml.Unmarshal(head, &g); err != nil {
		return Guide{}, err
	}
	g.Body = strings.TrimSpace(string(body))
	g.Preview, _, _ = strings.Cut(g.Body, "\n\n")
	if g.Title == "" || g.Preview == "" {
		return Guide{}, errors.New("a title and a body are required")
	}
	return g, nil
}

// List is every guide.
func (c Catalog) List() []Guide { return c.guides }

// Get finds a guide.
func (c Catalog) Get(slug string) (Guide, error) {
	for _, g := range c.guides {
		if g.Slug == slug {
			return g, nil
		}
	}
	return Guide{}, ErrNotFound
}
