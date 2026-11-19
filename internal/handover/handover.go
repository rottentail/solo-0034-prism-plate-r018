// Package handover seals rendered pages with their pinned source manuscripts
// into one deterministic, offline-inspectable archive.
package handover

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"prismplate/internal/impose"
	"prismplate/internal/repository"
	"prismplate/internal/scene"
	"sort"
	"time"
)

const Schema = "prism-plate-handover-v1"

// SourceDoc is one manuscript revision carried verbatim for offline provenance.
type SourceDoc struct {
	Plate    string         `json:"plate"`
	Revision int            `json:"revision"`
	Digest   string         `json:"digest"`
	File     string         `json:"file"`
	Document scene.Document `json:"document"`
}

// Manifest maps every page image back to its exact source pins and explains
// placement fit, clipping and safe-area outcomes.
type Manifest struct {
	Schema  string      `json:"schema"`
	Book    BookRef     `json:"book"`
	Pages   []PageEntry `json:"pages"`
	Sources []SourceDoc `json:"sources"`
	Notes   []string    `json:"notes,omitempty"`
}

type BookRef struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Revision int    `json:"revision"`
	Digest   string `json:"digest"`
}

// PageEntry ties one image file to the exact page declaration that produced it.
// Page is the verbatim scene.Page, so a reviewer can re-derive bounds and pins
// without the original service.
type PageEntry struct {
	Number      int                          `json:"number"`
	File        string                       `json:"file"`
	Digest      string                       `json:"digest"`
	Width       int                          `json:"width"`
	Height      int                          `json:"height"`
	SafeMargin  float64                      `json:"safe_margin"`
	SafeViolate bool                         `json:"safe_violated"`
	Page        scene.Page                   `json:"page"`
	Placements  []repository.PlacementReport `json:"placements"`
}

// Build assembles page images, their pinned source documents and a manifest
// into a stored ZIP. Entry order, timestamps and modes are fixed so equal input
// yields equal bytes.
func Build(book repository.Book, rev repository.BookRevision, pages []impose.PageResult, docs map[repository.Pin]scene.Document) ([]byte, Manifest, error) {
	if len(pages) != len(rev.Pages) {
		return nil, Manifest{}, fmt.Errorf("handover requires one rendered image per page")
	}
	m := Manifest{Schema: Schema, Book: BookRef{ID: book.ID, Title: book.Title, Revision: rev.Number, Digest: rev.Digest}}
	pins := map[repository.Pin]bool{}
	for i := range pages {
		entry := PageEntry{
			Number:      i + 1,
			File:        fmt.Sprintf("pages/page-%03d.png", i+1),
			Digest:      pages[i].Digest,
			Width:       pages[i].Width,
			Height:      pages[i].Height,
			SafeMargin:  pages[i].SafeMargin,
			SafeViolate: pages[i].SafeViolate,
			Page:        rev.Pages[i],
			Placements:  pages[i].Placements,
		}
		if entry.Placements == nil {
			entry.Placements = []impose.PlacementReport{}
		}
		for _, pl := range entry.Placements {
			pins[pl.Pin] = true
		}
		m.Pages = append(m.Pages, entry)
	}
	ordered := make([]repository.Pin, 0, len(pins))
	for p := range pins {
		ordered = append(ordered, p)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Plate != ordered[j].Plate {
			return ordered[i].Plate < ordered[j].Plate
		}
		return ordered[i].Revision < ordered[j].Revision
	})
	for _, p := range ordered {
		doc, ok := docs[p]
		if !ok {
			return nil, Manifest{}, fmt.Errorf("missing source document for %s revision %d", p.Plate, p.Revision)
		}
		if repository.SceneHash(doc) != p.Digest {
			return nil, Manifest{}, fmt.Errorf("source document digest mismatch for %s revision %d", p.Plate, p.Revision)
		}
		m.Sources = append(m.Sources, SourceDoc{
			Plate:    p.Plate,
			Revision: p.Revision,
			Digest:   p.Digest,
			File:     fmt.Sprintf("sources/%s-r%03d.json", p.Plate, p.Revision),
			Document: doc,
		})
	}
	if len(m.Pages) == 0 {
		return nil, Manifest{}, fmt.Errorf("handover requires at least one page")
	}
	if m.Sources == nil {
		m.Sources = []SourceDoc{}
	}
	m.Notes = []string{
		"Each page image is the canonical four-sample PNG of the stated book revision.",
		"Page declarations are included verbatim, so placement order, fit, rotation and span can be re-derived offline.",
		"Placement reports give the resolved scale, rotated bounds, clipped area and safe-margin verdict per draw.",
		"Source documents are stored verbatim; their SHA-256 over compact JSON equals the pin digest.",
		"Cross-page placements record role=anchor on the declaring page and role=continuation on later slices sharing one transform.",
	}
	encoded, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, Manifest{}, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name string, data []byte) error {
		if path.Clean(name) != name || path.IsAbs(name) {
			return fmt.Errorf("unsafe archive path %q", name)
		}
		h := &zip.FileHeader{Name: name, Method: zip.Store, Modified: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)}
		h.SetMode(0o644)
		w, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}
	if err := write("manifest.json", encoded); err != nil {
		return nil, Manifest{}, err
	}
	for i := range pages {
		if err := write(m.Pages[i].File, pages[i].PNG); err != nil {
			return nil, Manifest{}, err
		}
	}
	for _, s := range m.Sources {
		raw, err := json.Marshal(s.Document)
		if err != nil {
			return nil, Manifest{}, err
		}
		if err := write(s.File, raw); err != nil {
			return nil, Manifest{}, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, Manifest{}, err
	}
	if buf.Len() > 96<<20 {
		return nil, Manifest{}, fmt.Errorf("handover exceeds 96MiB")
	}
	return buf.Bytes(), m, nil
}
