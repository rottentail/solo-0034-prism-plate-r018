package service

import (
	"context"
	"fmt"
	"prismplate/internal/exposure"
	"prismplate/internal/handover"
	"prismplate/internal/impose"
	"prismplate/internal/raster"
	"prismplate/internal/repository"
	"prismplate/internal/scene"
	"prismplate/internal/validate"
	"strconv"
	"time"
)

// pin resolves an explicit plate/revision reference and verifies its digest.
// An empty digest is filled from the current facts; a supplied digest that no
// longer matches is rejected instead of being silently retargeted.
func (s *Service) pin(plate string, revision int, digest string) (repository.Pin, scene.Document, error) {
	return pinIn(s.Repo.View(), plate, revision, digest)
}

// pinIn is the single pin rule. It is shared by admission and composition so a
// placement cannot be admitted under one rule and drawn under another.
func pinIn(st repository.State, plate string, revision int, digest string) (repository.Pin, scene.Document, error) {
	if !validate.ID(plate) || revision < 1 {
		return repository.Pin{}, scene.Document{}, fmt.Errorf("placement requires a plate and positive revision")
	}
	p, ok := st.Plates[plate]
	if !ok {
		return repository.Pin{}, scene.Document{}, repository.ErrMissing
	}
	if revision > len(p.Revisions) {
		return repository.Pin{}, scene.Document{}, repository.ErrMissing
	}
	r := p.Revisions[revision-1]
	if digest != "" && digest != r.Digest {
		return repository.Pin{}, scene.Document{}, fmt.Errorf("placement %s revision %d: %w", plate, revision, repository.ErrSource)
	}
	return repository.Pin{Plate: plate, Revision: revision, Digest: r.Digest}, r.Document, nil
}

// resolvePages fills in every empty pin digest and revalidates the page set.
// Pins are checked against facts at admission time, so a book revision can
// never start life pointing at a revision that does not exist.
func (s *Service) resolvePages(pages []scene.Page) ([]scene.Page, error) {
	if err := validate.Pages(pages); err != nil {
		return nil, err
	}
	sizes := map[[3]string]int{}
	for i := range pages {
		for j := range pages[i].Placements {
			pl := &pages[i].Placements[j]
			p, doc, err := s.pin(pl.Plate, pl.Revision, pl.Digest)
			if err != nil {
				return nil, fmt.Errorf("page %d placement %d: %w", i+1, j+1, err)
			}
			pl.Digest = p.Digest
			sizes[[3]string{p.Plate, strconv.Itoa(p.Revision), p.Digest}] = doc.Width * doc.Height
		}
	}
	if err := validate.Sources(sizes); err != nil {
		return nil, err
	}
	return pages, nil
}

func (s *Service) CreateBook(id, title string, pages []scene.Page) (repository.Book, error) {
	if !validate.ID(id) || len(title) == 0 || len(title) > 160 {
		return repository.Book{}, fmt.Errorf("invalid book identity")
	}
	resolved, err := s.resolvePages(pages)
	if err != nil {
		return repository.Book{}, err
	}
	b := repository.Book{
		ID:    id,
		Title: title,
		State: "active",
		Revisions: []repository.BookRevision{{
			Number: 1, Digest: repository.BookHash(resolved), Pages: resolved, Created: time.Now().UTC(),
		}},
	}
	err = s.Repo.Update(func(st *repository.State) error {
		if _, ok := st.Books[id]; ok {
			return repository.ErrConflict
		}
		st.Books[id] = b
		return nil
	})
	return b, err
}

func (s *Service) Book(id string) (repository.Book, error) {
	b, ok := s.Repo.View().Books[id]
	if !ok {
		return b, repository.ErrMissing
	}
	return b, nil
}

func (s *Service) ReviseBook(id string, expected int, pages []scene.Page) (repository.Book, error) {
	resolved, err := s.resolvePages(pages)
	if err != nil {
		return repository.Book{}, err
	}
	var b repository.Book
	err = s.Repo.Update(func(st *repository.State) error {
		var ok bool
		b, ok = st.Books[id]
		if !ok {
			return repository.ErrMissing
		}
		if b.State != "active" {
			return repository.ErrState
		}
		if len(b.Revisions) != expected {
			return repository.ErrConflict
		}
		if len(b.Revisions) >= validate.MaxBookRevisions {
			return fmt.Errorf("book revision capacity reached")
		}
		b.Revisions = append(b.Revisions, repository.BookRevision{
			Number: expected + 1, Digest: repository.BookHash(resolved), Pages: resolved, Created: time.Now().UTC(),
		})
		st.Books[id] = b
		return nil
	})
	return b, err
}

// Reorder returns a new book revision with pages permuted by order (1-based
// page numbers). Placements travel with their page, so a source can never be
// re-attached to a different position than the one it was reviewed at.
func (s *Service) ReorderBook(id string, expected int, order []int) (repository.Book, error) {
	var b repository.Book
	err := s.Repo.Update(func(st *repository.State) error {
		var ok bool
		b, ok = st.Books[id]
		if !ok {
			return repository.ErrMissing
		}
		if b.State != "active" {
			return repository.ErrState
		}
		if len(b.Revisions) != expected {
			return repository.ErrConflict
		}
		cur := b.Revisions[len(b.Revisions)-1]
		if len(order) != len(cur.Pages) {
			return fmt.Errorf("reorder must list every page exactly once")
		}
		seen := make([]bool, len(cur.Pages))
		pages := make([]scene.Page, len(order))
		for i, n := range order {
			if n < 1 || n > len(cur.Pages) || seen[n-1] {
				return fmt.Errorf("reorder must list every page exactly once")
			}
			seen[n-1] = true
			pages[i] = cur.Pages[n-1]
		}
		if err := scene.CheckReorder(cur.Pages, order); err != nil {
			return err
		}
		b.Revisions = append(b.Revisions, repository.BookRevision{
			Number: expected + 1, Digest: repository.BookHash(pages), Pages: pages, Created: time.Now().UTC(),
		})
		st.Books[id] = b
		return nil
	})
	return b, err
}

func (s *Service) ArchiveBook(id string) error {
	return s.Repo.Update(func(st *repository.State) error {
		b, ok := st.Books[id]
		if !ok {
			return repository.ErrMissing
		}
		b.State = "archived"
		st.Books[id] = b
		return nil
	})
}

func (s *Service) captureBook(id string, revision int) (repository.BookRevision, error) {
	b, err := s.Book(id)
	if err != nil {
		return repository.BookRevision{}, err
	}
	if revision < 1 || revision > len(b.Revisions) {
		return repository.BookRevision{}, repository.ErrMissing
	}
	return b.Revisions[revision-1], nil
}

// composeBook rasterizes every pinned source once per book revision and asks
// impose to place it. Source facts are captured before composition starts, so a
// concurrent revision elsewhere cannot change this book revision's output. Only
// the pages selected by want are composed; a nil want means every page.
func (s *Service) composeBook(ctx context.Context, rev repository.BookRevision, want map[int]bool) ([]impose.PageResult, map[repository.Pin]scene.Document, error) {
	if want == nil {
		want = map[int]bool{}
		for i := range rev.Pages {
			want[i] = true
		}
	}
	needed := map[repository.Pin]bool{}
	for i := range rev.Pages {
		if !want[i] {
			continue
		}
		for _, d := range scene.Plan(rev.Pages, i) {
			pl := d.Placement
			needed[repository.Pin{Plate: pl.Plate, Revision: pl.Revision, Digest: pl.Digest}] = true
		}
	}
	docs := map[repository.Pin]scene.Document{}
	canvases := map[repository.Pin]*raster.Canvas{}
	sizes := map[repository.Pin][2]int{}
	// One snapshot fixes every source fact for this composition pass.
	state := s.Repo.View()
	for p := range needed {
		got, doc, err := pinIn(state, p.Plate, p.Revision, p.Digest)
		if err != nil {
			return nil, nil, err
		}
		if got != p {
			return nil, nil, fmt.Errorf("book source pin no longer matches persisted facts")
		}
		c, err := validate.Compile(doc)
		if err != nil {
			return nil, nil, err
		}
		cv, err := raster.RenderCanvas(ctx, c)
		if err != nil {
			return nil, nil, err
		}
		canvases[p] = cv
		sizes[p] = [2]int{cv.Width, cv.Height}
		docs[p] = doc
	}
	results := make([]impose.PageResult, len(rev.Pages))
	for i, page := range rev.Pages {
		if !want[i] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		draws := scene.Plan(rev.Pages, i)
		srcs := make([]impose.Source, len(draws))
		for j, d := range draws {
			p := repository.Pin{Plate: d.Placement.Plate, Revision: d.Placement.Revision, Digest: d.Placement.Digest}
			size := sizes[p]
			srcs[j] = impose.Source{Pin: p, Canvas: canvases[p], Width: size[0], Height: size[1]}
		}
		res, err := impose.RenderPage(ctx, page, draws, srcs)
		if err != nil {
			return nil, nil, err
		}
		res.Number = i + 1
		results[i] = res
	}
	return results, docs, nil
}

// RenderPage renders one page of a book revision and commits the image with its
// source pins. Identity is content-addressed over the exact pins, so swapping
// two pages changes every affected page's identity rather than reusing one.
func (s *Service) RenderPage(ctx context.Context, id string, revision, page int) (repository.PageArtifact, bool, error) {
	b, err := s.Book(id)
	if err != nil {
		return repository.PageArtifact{}, false, err
	}
	rev, err := s.captureBook(id, revision)
	if err != nil {
		return repository.PageArtifact{}, false, err
	}
	if page < 1 || page > len(rev.Pages) {
		return repository.PageArtifact{}, false, repository.ErrMissing
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		return repository.PageArtifact{}, false, ctx.Err()
	default:
		return repository.PageArtifact{}, false, exposure.ErrBusy
	}
	results, _, err := s.composeBook(ctx, rev, map[int]bool{page - 1: true})
	if err != nil {
		return repository.PageArtifact{}, false, err
	}
	return s.commitPage(ctx, b, rev, results[page-1], page)
}

// pageRecord builds the persisted artifact for one composed page.
func (s *Service) pageRecord(b repository.Book, rev repository.BookRevision, res impose.PageResult, page int) repository.PageArtifact {
	pins := repository.Pins(rev.Pages, page-1)
	key := repository.Hash([]byte(b.ID + ":" + strconv.Itoa(rev.Number) + ":" + rev.Digest + ":" + strconv.Itoa(page) + ":" + res.Digest + ":png"))
	placements := res.Placements
	if placements == nil {
		placements = []repository.PlacementReport{}
	}
	return repository.PageArtifact{
		ID: key, Book: b.ID, BookRevision: rev.Number, BookDigest: rev.Digest, Page: page,
		Format: "png", Digest: res.Digest, Data: res.PNG, State: "available", Sources: pins,
		Report: repository.PageReport{SafeMargin: res.SafeMargin, SafeViolated: res.SafeViolate, Placements: placements},
	}
}

// commitPages publishes complete page images in one snapshot update. An
// existing available record is reused; a revoked one blocks the commit rather
// than being silently reactivated. Nothing partial is ever written.
func (s *Service) commitPages(ctx context.Context, records []repository.PageArtifact) ([]repository.PageArtifact, []bool, error) {
	created := make([]bool, len(records))
	out := make([]repository.PageArtifact, len(records))
	err := s.Repo.Update(func(st *repository.State) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		for i, rec := range records {
			if old, ok := st.PageArtifacts[rec.ID]; ok {
				if old.State != "available" {
					return repository.ErrState
				}
				out[i] = old
				continue
			}
			st.PageArtifacts[rec.ID] = rec
			out[i] = rec
			created[i] = true
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return out, created, nil
}

func (s *Service) commitPage(ctx context.Context, b repository.Book, rev repository.BookRevision, res impose.PageResult, page int) (repository.PageArtifact, bool, error) {
	rec := s.pageRecord(b, rev, res, page)
	out, created, err := s.commitPages(ctx, []repository.PageArtifact{rec})
	if err != nil {
		return repository.PageArtifact{}, false, err
	}
	return out[0], created[0], nil
}

// RenderBook renders every page of one book revision. Pages are composed from a
// single captured revision, so a mid-render revision elsewhere cannot mix
// selection rounds. A failure is reported without committing partial pages.
func (s *Service) RenderBook(ctx context.Context, id string, revision int) ([]repository.PageArtifact, error) {
	b, err := s.Book(id)
	if err != nil {
		return nil, err
	}
	rev, err := s.captureBook(id, revision)
	if err != nil {
		return nil, err
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return nil, exposure.ErrBusy
	}
	results, _, err := s.composeBook(ctx, rev, nil)
	if err != nil {
		return nil, err
	}
	records := make([]repository.PageArtifact, len(results))
	for i := range results {
		records[i] = s.pageRecord(b, rev, results[i], i+1)
	}
	out, _, err := s.commitPages(ctx, records)
	return out, err
}

func (s *Service) PageArtifact(id string) (repository.PageArtifact, error) {
	a, ok := s.Repo.View().PageArtifacts[id]
	if !ok {
		return a, repository.ErrMissing
	}
	if a.State != "available" {
		return a, repository.ErrState
	}
	return a, nil
}

func (s *Service) RevokePage(id string) error {
	return s.Repo.Update(func(st *repository.State) error {
		a, ok := st.PageArtifacts[id]
		if !ok {
			return repository.ErrMissing
		}
		a.State = "revoked"
		st.PageArtifacts[id] = a
		return nil
	})
}

// Handover composes the revision fresh and returns a deterministic archive plus
// its manifest. It does not depend on previously committed page artifacts, so
// a handover always reflects the named revision and its exact pins.
func (s *Service) Handover(ctx context.Context, id string, revision int) ([]byte, handover.Manifest, error) {
	b, err := s.Book(id)
	if err != nil {
		return nil, handover.Manifest{}, err
	}
	rev, err := s.captureBook(id, revision)
	if err != nil {
		return nil, handover.Manifest{}, err
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		return nil, handover.Manifest{}, ctx.Err()
	default:
		return nil, handover.Manifest{}, exposure.ErrBusy
	}
	results, docs, err := s.composeBook(ctx, rev, nil)
	if err != nil {
		return nil, handover.Manifest{}, err
	}
	return handover.Build(b, rev, results, docs)
}
