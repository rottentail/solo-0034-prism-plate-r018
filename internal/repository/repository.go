// Package repository commits complete immutable snapshots under a single writer.
package repository

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"prismplate/internal/geom"
	"prismplate/internal/scene"
	"sync"
	"time"
)

var ErrConflict = errors.New("revision conflict")
var ErrMissing = errors.New("not found")
var ErrState = errors.New("invalid lifecycle state")

type Revision struct {
	Number   int            `json:"number"`
	Digest   string         `json:"digest"`
	Document scene.Document `json:"document"`
	Created  time.Time      `json:"created"`
}
type Plate struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	State     string     `json:"state"`
	Revisions []Revision `json:"revisions"`
}
type Artifact struct {
	ID          string `json:"id"`
	Plate       string `json:"plate"`
	Revision    int    `json:"revision"`
	SceneDigest string `json:"scene_digest"`
	Format      string `json:"format"`
	Digest      string `json:"digest"`
	State       string `json:"state"`
	Data        []byte `json:"data"`
}
type Proof struct {
	ID       string `json:"id"`
	Artifact string `json:"artifact"`
	State    string `json:"state"`
	Note     string `json:"note"`
}

// Pin is an explicit, digest-checked reference to one immutable plate revision.
type Pin struct {
	Plate    string `json:"plate"`
	Revision int    `json:"revision"`
	Digest   string `json:"digest"`
}
type BookRevision struct {
	Number  int          `json:"number"`
	Digest  string       `json:"digest"`
	Pages   []scene.Page `json:"pages"`
	Created time.Time    `json:"created"`
}
type Book struct {
	ID        string         `json:"id"`
	Title     string         `json:"title"`
	State     string         `json:"state"`
	Revisions []BookRevision `json:"revisions"`
}

// PlacementReport explains how one placement resolved onto a page. It is stored
// beside each page image so overflow and safe-area outcomes stay auditable.
type PlacementReport struct {
	Index int    `json:"index"`
	Pin   Pin    `json:"pin"`
	Fit   string `json:"fit"`
	// Role is single, anchor or continuation for cross-page spreads. AnchorPage
	// names the page that declared the placement; Slice and Span are 1-based.
	Role       string `json:"role"`
	AnchorPage int    `json:"anchor_page"`
	Slice      int    `json:"slice"`
	Span       int    `json:"span"`
	// ScaleX/ScaleY are page pixels per source pixel after fit and rotation.
	ScaleX float64 `json:"scale_x"`
	ScaleY float64 `json:"scale_y"`
	// Content is the fitted source extent before rotation, in page coordinates.
	Content geom.Rect `json:"content"`
	// Bounds is the rotated placement box's axis-aligned extent.
	Bounds geom.Rect `json:"bounds"`
	// ClippedArea is the rotated box area outside the trim box, in page px².
	ClippedArea float64 `json:"clipped_area"`
	// Overflow is the per-edge distance the rotated box extends past trim.
	Overflow geom.Edges `json:"overflow"`
	// ExceedsSafeArea is true when the rotated box leaves the cut-safe inset.
	ExceedsSafeArea bool `json:"exceeds_safe_area"`
	// Letterbox is box area left uncovered by source content under "contain".
	Letterbox float64 `json:"letterbox"`
	// OffPage is true when no part of the placement reaches the trim box.
	OffPage bool `json:"off_page"`
}

// PageReport is the page-level composition verdict persisted with an image.
type PageReport struct {
	SafeMargin   float64           `json:"safe_margin"`
	SafeViolated bool              `json:"safe_violated"`
	Placements   []PlacementReport `json:"placements"`
}

// PageArtifact is a rendered page image bound to one book revision and to the
// exact source pins that produced it.
type PageArtifact struct {
	ID           string     `json:"id"`
	Book         string     `json:"book"`
	BookRevision int        `json:"book_revision"`
	BookDigest   string     `json:"book_digest"`
	Page         int        `json:"page"`
	Format       string     `json:"format"`
	Digest       string     `json:"digest"`
	Data         []byte     `json:"data"`
	State        string     `json:"state"`
	Sources      []Pin      `json:"sources"`
	Report       PageReport `json:"report"`
}
type State struct {
	Schema        string                  `json:"schema"`
	Plates        map[string]Plate        `json:"plates"`
	Books         map[string]Book         `json:"books"`
	Artifacts     map[string]Artifact     `json:"artifacts"`
	PageArtifacts map[string]PageArtifact `json:"page_artifacts"`
	Proofs        map[string]Proof        `json:"proofs"`
}

var ErrSource = errors.New("source revision mismatch")

type Repo struct {
	mu    sync.RWMutex
	root  string
	state State
	lock  *os.File
}

func Hash(b []byte) string               { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }
func SceneHash(d scene.Document) string  { b, _ := json.Marshal(d); return Hash(b) }
func BookHash(pages []scene.Page) string { b, _ := json.Marshal(pages); return Hash(b) }
func Fresh() State {
	return State{Schema: "prism-plate-v1", Plates: map[string]Plate{}, Books: map[string]Book{}, Artifacts: map[string]Artifact{}, PageArtifacts: map[string]PageArtifact{}, Proofs: map[string]Proof{}}
}
func Open(root string) (*Repo, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(root, "writer.lock"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, fmt.Errorf("data root already locked; after an unclean exit verify no writer remains before removing writer.lock: %w", err)
	}
	r := &Repo{root: root, state: Fresh(), lock: lock}
	fmt.Fprintln(lock, os.Getpid())
	b, err := os.ReadFile(filepath.Join(root, "state.json"))
	if err == nil {
		if len(b) > 128<<20 {
			err = fmt.Errorf("snapshot too large")
		} else {
			err = json.Unmarshal(b, &r.state)
		}
		if err == nil {
			// Snapshots written before books existed omit the newer maps.
			if r.state.Books == nil {
				r.state.Books = map[string]Book{}
			}
			if r.state.PageArtifacts == nil {
				r.state.PageArtifacts = map[string]PageArtifact{}
			}
			err = Check(r.state)
		}
	}
	if err != nil && !os.IsNotExist(err) {
		r.Close()
		return nil, err
	}
	return r, nil
}

// Check verifies internal lineage before a restored state can become authoritative.
func Check(s State) error {
	if s.Schema != "prism-plate-v1" || s.Plates == nil || s.Books == nil || s.Artifacts == nil || s.PageArtifacts == nil || s.Proofs == nil {
		return fmt.Errorf("unsupported snapshot schema")
	}
	for id, p := range s.Plates {
		if p.ID != id || len(p.Revisions) == 0 || (p.State != "active" && p.State != "archived") {
			return fmt.Errorf("invalid plate lineage")
		}
		for i, r := range p.Revisions {
			if r.Number != i+1 || r.Digest != SceneHash(r.Document) {
				return fmt.Errorf("invalid revision digest")
			}
		}
	}
	for id, a := range s.Artifacts {
		p, ok := s.Plates[a.Plate]
		if !ok || a.ID != id || a.Revision < 1 || a.Revision > len(p.Revisions) || a.SceneDigest != p.Revisions[a.Revision-1].Digest || Hash(a.Data) != a.Digest || (a.State != "available" && a.State != "revoked") || (a.Format != "png" && a.Format != "svg") {
			return fmt.Errorf("invalid artifact lineage")
		}
	}
	for id, b := range s.Books {
		if b.ID != id || len(b.Revisions) == 0 || (b.State != "active" && b.State != "archived") {
			return fmt.Errorf("invalid book lineage")
		}
		for i, r := range b.Revisions {
			if r.Number != i+1 || r.Digest != BookHash(r.Pages) || len(r.Pages) == 0 || len(r.Pages) > 32 {
				return fmt.Errorf("invalid book revision digest")
			}
			if err := scene.CheckSpans(r.Pages); err != nil {
				return fmt.Errorf("invalid book spreads: %w", err)
			}
			for _, pg := range r.Pages {
				if pg.Width < 1 || pg.Width > 1024 || pg.Height < 1 || pg.Height > 1024 || len(pg.Placements) > 16 {
					return fmt.Errorf("invalid book page geometry")
				}
				if pg.SafeMargin < 0 || 2*pg.SafeMargin >= float64(pg.Width) || 2*pg.SafeMargin >= float64(pg.Height) {
					return fmt.Errorf("invalid book page safe margin")
				}
				if !(pg.Background.R >= 0 && pg.Background.R <= 1 && pg.Background.G >= 0 && pg.Background.G <= 1 && pg.Background.B >= 0 && pg.Background.B <= 1 && pg.Background.A >= 0 && pg.Background.A <= 1) {
					return fmt.Errorf("invalid book page background")
				}
				for _, pl := range pg.Placements {
					src, ok := s.Plates[pl.Plate]
					if !ok || pl.Revision < 1 || pl.Revision > len(src.Revisions) || src.Revisions[pl.Revision-1].Digest != pl.Digest {
						return fmt.Errorf("invalid book source pin")
					}
					if !(pl.Width > 0 && pl.Height > 0) || (pl.Fit != "contain" && pl.Fit != "cover" && pl.Fit != "stretch") || pl.Span < 0 || pl.Span > 16 {
						return fmt.Errorf("invalid book placement geometry")
					}
				}
			}
		}
	}
	for id, a := range s.PageArtifacts {
		b, ok := s.Books[a.Book]
		if !ok || a.ID != id || a.BookRevision < 1 || a.BookRevision > len(b.Revisions) || a.BookDigest != b.Revisions[a.BookRevision-1].Digest || a.Page < 1 || a.Page > len(b.Revisions[a.BookRevision-1].Pages) || a.Format != "png" || Hash(a.Data) != a.Digest || (a.State != "available" && a.State != "revoked") {
			return fmt.Errorf("invalid page artifact lineage")
		}
		pins := Pins(b.Revisions[a.BookRevision-1].Pages, a.Page-1)
		if len(a.Sources) != len(pins) {
			return fmt.Errorf("invalid page artifact sources")
		}
		for i := range pins {
			if pins[i] != a.Sources[i] {
				return fmt.Errorf("invalid page artifact sources")
			}
		}
		if len(a.Report.Placements) != len(pins) || a.Report.SafeMargin != b.Revisions[a.BookRevision-1].Pages[a.Page-1].SafeMargin {
			return fmt.Errorf("invalid page artifact report")
		}
		for i := range pins {
			if a.Report.Placements[i].Pin != pins[i] {
				return fmt.Errorf("invalid page artifact report")
			}
		}
	}
	for id, p := range s.Proofs {
		_, ok := s.Artifacts[p.Artifact]
		if p.ID != id || !ok || (p.State != "open" && p.State != "accepted" && p.State != "rejected") {
			return fmt.Errorf("invalid proof lineage")
		}
	}
	return nil
}

// Pins lists the sources actually painted onto a page, in paint order. Cross-
// page placements contribute a pin to every page they touch, so the recorded
// lineage explains continuation slices too.
func Pins(pages []scene.Page, index int) []Pin {
	draws := scene.Plan(pages, index)
	out := make([]Pin, 0, len(draws))
	for _, d := range draws {
		pl := d.Placement
		out = append(out, Pin{Plate: pl.Plate, Revision: pl.Revision, Digest: pl.Digest})
	}
	return out
}
func clone(s State) State   { b, _ := json.Marshal(s); var d State; _ = json.Unmarshal(b, &d); return d }
func (r *Repo) View() State { r.mu.RLock(); defer r.mu.RUnlock(); return clone(r.state) }

// Update keeps failed persistence invisible. Rename is the commit point; directory
// sync is best-effort because failure after rename cannot be represented as rollback.
func (r *Repo) Update(fn func(*State) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lock == nil {
		return ErrState
	}
	next := clone(r.state)
	if err := fn(&next); err != nil {
		return err
	}
	if err := Check(next); err != nil {
		return err
	}
	if len(next.Plates) > 128 || len(next.Books) > 64 || len(next.Artifacts) > 256 || len(next.PageArtifacts) > 256 || len(next.Proofs) > 1024 {
		return fmt.Errorf("snapshot object capacity reached")
	}
	b, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if len(b) > 128<<20 {
		return fmt.Errorf("snapshot byte capacity reached")
	}
	f, err := os.CreateTemp(r.root, "state-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(name, filepath.Join(r.root, "state.json")); err != nil {
		return err
	}
	r.state = next
	if dir, e := os.Open(r.root); e == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}
func (r *Repo) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lock == nil {
		return nil
	}
	err := r.lock.Close()
	r.lock = nil
	if e := os.Remove(filepath.Join(r.root, "writer.lock")); err == nil {
		err = e
	}
	return err
}
