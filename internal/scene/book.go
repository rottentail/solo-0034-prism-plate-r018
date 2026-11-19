package scene

import (
	"fmt"
	"math"

	"prismplate/internal/geom"
)

// Placement pins one explicit plate revision into a page box. Digest is the
// referenced revision digest, so a page cannot silently follow a newer revision.
//
// Span > 1 continues the same placement across consecutive pages. The box is
// fitted once to the combined spread, rotated once about the spread center, and
// then cut by the page boundaries. A graphic crossing the gutter therefore keeps
// a single continuous transform and is sliced rather than re-fitted per page.
//
// X is measured from the anchor page's left edge; the placement occupies
// [X, X+Width*Span] of the spread and must stay inside the spread width.
type Placement struct {
	Plate    string  `json:"plate"`
	Revision int     `json:"revision"`
	Digest   string  `json:"digest"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Width    float64 `json:"width"`
	Height   float64 `json:"height"`
	Rotation float64 `json:"rotation"`
	Fit      string  `json:"fit"`
	Span     int     `json:"span,omitempty"`
}

// Page is a fixed-size proof page. SafeMargin is the cut safety inset measured
// inward from the trim box; placements are never moved automatically.
type Page struct {
	Width      int         `json:"width"`
	Height     int         `json:"height"`
	Background Color       `json:"background"`
	SafeMargin float64     `json:"safe_margin"`
	Placements []Placement `json:"placements,omitempty"`
}

// SpanOf normalises the optional span to at least one page.
func (p Placement) SpanOf() int {
	if p.Span < 1 {
		return 1
	}
	return p.Span
}

// Draw is one page's share of one placement. The anchor page declares the
// placement; continuation pages receive later slices of the same transform.
type Draw struct {
	Placement Placement
	// Anchor is the 0-based page index that declared the placement.
	Anchor int
	// Slice is the 0-based slice index within the span.
	Slice int
	// Total is the placement's span in pages.
	Total int
}

// Role names the draw's part in the spread for reporting.
func (d Draw) Role() string {
	switch {
	case d.Total <= 1:
		return "single"
	case d.Slice == 0:
		return "anchor"
	default:
		return "continuation"
	}
}

// Plan returns the draws that paint page index in deterministic order: slices
// continued from earlier pages first, then the page's own placements in
// declaration order with each span contributing its own slice.
func Plan(pages []Page, index int) []Draw {
	var inherited, own []Draw
	for a := 0; a <= index && a < len(pages); a++ {
		for _, pl := range pages[a].Placements {
			total := pl.SpanOf()
			if index < a || index >= a+total {
				continue
			}
			d := Draw{Placement: pl, Anchor: a, Slice: index - a, Total: total}
			if a == index {
				own = append(own, d)
			} else {
				inherited = append(inherited, d)
			}
		}
	}
	return append(inherited, own...)
}

// CheckSpans admits cross-page placements against the ordered page set. It is
// shared by book admission, reorder and restored-snapshot checks so no path can
// create a span whose slices would be silently dropped or resized.
func CheckSpans(pages []Page) error {
	for a, page := range pages {
		for j, pl := range page.Placements {
			total := pl.SpanOf()
			if total <= 1 {
				continue
			}
			if total > 16 {
				return fmt.Errorf("page %d placement %d: span must be 1..16 pages", a+1, j+1)
			}
			if a+total > len(pages) {
				return fmt.Errorf("page %d placement %d: span %d exceeds the remaining pages", a+1, j+1, total)
			}
			spreadWidth := float64(page.Width) * float64(total)
			if pl.X < 0 || pl.X+pl.Width*float64(total) > spreadWidth {
				return fmt.Errorf("page %d placement %d: cross-page box must stay inside its %d-page spread", a+1, j+1, total)
			}
			for k := 1; k < total; k++ {
				next := pages[a+k]
				if next.Width != page.Width || next.Height != page.Height {
					return fmt.Errorf("page %d placement %d: pages spanned by a cross-page placement must share trim size", a+1, j+1)
				}
			}
		}
	}
	return nil
}

// Spreads lists the page ranges [start,end) that must remain contiguous: every
// cross-page placement pins its anchor and continuations together.
func Spreads(pages []Page) [][2]int {
	var out [][2]int
	for a, page := range pages {
		end := a + 1
		for _, pl := range page.Placements {
			if e := a + pl.SpanOf(); e > end {
				end = e
			}
		}
		if end > a+1 && end <= len(pages) {
			out = append(out, [2]int{a, end})
		}
	}
	return out
}

// CheckReorder verifies that a new page order keeps every cross-page spread
// contiguous and in its original internal order. Whole spreads may move; a
// spread may not be split, because its slices share one source transform.
// order holds 1-based original page numbers in their new positions.
func CheckReorder(old []Page, order []int) error {
	if len(old) != len(order) {
		return fmt.Errorf("reorder must list every page exactly once")
	}
	used := make([]bool, len(old))
	at := make(map[int]int, len(order))
	for k, n := range order {
		id := n - 1
		if id < 0 || id >= len(old) || used[id] {
			return fmt.Errorf("reorder must list every page exactly once")
		}
		used[id] = true
		at[id] = k
	}
	for _, sp := range Spreads(old) {
		for i := sp[0]; i < sp[1]; i++ {
			if at[i] != at[sp[0]]+(i-sp[0]) {
				return fmt.Errorf("page %d belongs to a cross-page spread; move its pages together and in order", i+1)
			}
		}
	}
	return CheckSpans(reorderedPages(old, order))
}

func reorderedPages(old []Page, order []int) []Page {
	out := make([]Page, len(order))
	for i, n := range order {
		out[i] = old[n-1]
	}
	return out
}

// SpreadMatrix maps source pixel coordinates to a page's coordinates for one
// draw. Page k's coordinates are the spread translated left by k page widths.
func SpreadMatrix(pl Placement, pageWidth int, slice, sw, sh int) geom.Matrix {
	total := float64(pl.SpanOf())
	boxW := pl.Width * total
	sx, sy := FitScale(pl.Fit, sw, sh, boxW, pl.Height)
	return geom.Multiply(
		geom.Translate(pl.X+boxW/2-float64(slice)*float64(pageWidth), pl.Y+pl.Height/2),
		geom.Multiply(geom.Rotate(pl.Rotation), geom.Multiply(geom.ScaleMatrix(sx, sy), geom.Translate(-float64(sw)/2, -float64(sh)/2))),
	)
}

// FitScale returns per-axis source-to-box scaling. "stretch" is the only mode
// that distorts; contain/cover preserve the source aspect ratio.
func FitScale(mode string, sw, sh int, w, h float64) (float64, float64) {
	sx := w / float64(sw)
	sy := h / float64(sh)
	switch mode {
	case "cover":
		v := math.Max(sx, sy)
		return v, v
	case "stretch":
		return sx, sy
	default:
		v := math.Min(sx, sy)
		return v, v
	}
}

// DrawCorners returns the draw's rotated box corners in page coordinates.
func DrawCorners(pl Placement, pageWidth, slice int) []geom.Point {
	total := pl.SpanOf()
	boxW := pl.Width * float64(total)
	cx, cy := pl.X+boxW/2, pl.Y+pl.Height/2
	shift := float64(slice) * float64(pageWidth)
	r := geom.Rotate(pl.Rotation)
	corner := func(x, y float64) geom.Point {
		q := r.Apply(geom.Point{X: x - cx, Y: y - cy})
		return geom.Point{X: q.X + cx - shift, Y: q.Y + cy}
	}
	return []geom.Point{
		corner(pl.X, pl.Y),
		corner(pl.X+boxW, pl.Y),
		corner(pl.X+boxW, pl.Y+pl.Height),
		corner(pl.X, pl.Y+pl.Height),
	}
}
