package handover

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"prismplate/internal/repository"
	"prismplate/internal/scene"
)

// Verify re-derives every digest in a handover archive without the original
// service: page images must match their manifest digests, source documents must
// match their pin digests, page numbers must be dense and ordered, and each
// page's declared pins must match the recorded draw lineage.
func Verify(raw []byte) (Manifest, error) {
	var m Manifest
	if len(raw) > 128<<20 {
		return m, fmt.Errorf("handover too large")
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return m, fmt.Errorf("handover is not a readable archive: %w", err)
	}
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		if _, dup := files[f.Name]; dup {
			return m, fmt.Errorf("duplicate archive entry %q", f.Name)
		}
		files[f.Name] = f
	}
	read := func(name string) ([]byte, error) {
		f, ok := files[name]
		if !ok {
			return nil, fmt.Errorf("archive is missing %q", name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(io.LimitReader(rc, 128<<20))
	}
	rawManifest, err := read("manifest.json")
	if err != nil {
		return m, err
	}
	d := json.NewDecoder(bytes.NewReader(rawManifest))
	d.DisallowUnknownFields()
	if err := d.Decode(&m); err != nil {
		return m, fmt.Errorf("invalid manifest: %w", err)
	}
	if m.Schema != Schema {
		return m, fmt.Errorf("unsupported handover schema")
	}
	if len(m.Pages) == 0 {
		return m, fmt.Errorf("handover requires at least one page")
	}
	pages := make([]scene.Page, len(m.Pages))
	for i, p := range m.Pages {
		if p.Number != i+1 {
			return m, fmt.Errorf("page numbering must be dense and ordered")
		}
		data, err := read(p.File)
		if err != nil {
			return m, err
		}
		if repository.Hash(data) != p.Digest {
			return m, fmt.Errorf("page %d image digest mismatch", p.Number)
		}
		if len(data) < 8 || string(data[1:4]) != "PNG" {
			return m, fmt.Errorf("page %d is not a PNG image", p.Number)
		}
		pages[i] = p.Page
		if p.Page.Width != p.Width || p.Page.Height != p.Height {
			return m, fmt.Errorf("page %d declaration does not match its image size", p.Number)
		}
	}
	if err := scene.CheckSpans(pages); err != nil {
		return m, err
	}
	if repository.BookHash(pages) != m.Book.Digest {
		return m, fmt.Errorf("book digest does not match the delivered page declarations")
	}
	for i, p := range m.Pages {
		want := repository.Pins(pages, i)
		if len(want) != len(p.Placements) {
			return m, fmt.Errorf("page %d draw count does not match its images", p.Number)
		}
		for j := range want {
			if want[j] != p.Placements[j].Pin {
				return m, fmt.Errorf("page %d draw %d names the wrong source pin", p.Number, j+1)
			}
		}
	}
	sources := map[repository.Pin]bool{}
	for _, s := range m.Sources {
		raw, err := read(s.File)
		if err != nil {
			return m, err
		}
		embedded, err := json.Marshal(s.Document)
		if err != nil {
			return m, err
		}
		if !bytes.Equal(raw, embedded) {
			return m, fmt.Errorf("source %s revision %d does not match the manifest copy", s.Plate, s.Revision)
		}
		if repository.SceneHash(s.Document) != s.Digest {
			return m, fmt.Errorf("source %s revision %d digest mismatch", s.Plate, s.Revision)
		}
		sources[repository.Pin{Plate: s.Plate, Revision: s.Revision, Digest: s.Digest}] = true
	}
	// Every recorded draw must still resolve to a carried source document.
	for _, p := range m.Pages {
		for _, pl := range p.Placements {
			if !sources[pl.Pin] {
				return m, fmt.Errorf("page %d references source %s revision %d that the archive does not carry", p.Number, pl.Pin.Plate, pl.Pin.Revision)
			}
		}
	}
	return m, nil
}
