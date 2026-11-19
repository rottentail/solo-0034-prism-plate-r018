// Package impose composes pinned plate revisions into fixed-order proof pages.
package impose

import (
	"context"
	"fmt"
	"math"
	"prismplate/internal/geom"
	"prismplate/internal/paint"
	"prismplate/internal/raster"
	"prismplate/internal/repository"
	"prismplate/internal/scene"
)

// Source is one captured, immutable plate revision used by a placement.
type Source struct {
	Pin    repository.Pin
	Canvas *raster.Canvas
	Width  int
	Height int
}

// PlacementReport is the persisted, per-draw composition verdict.
type PlacementReport = repository.PlacementReport

// PageResult is one rendered page and its traceable source projection.
type PageResult struct {
	Number      int
	Width       int
	Height      int
	Digest      string
	Painter     string
	SafeMargin  float64
	SafeViolate bool
	Placements  []PlacementReport
	PNG         []byte
}

func trimBox(p scene.Page) geom.Rect {
	return geom.Rect{X: 0, Y: 0, W: float64(p.Width), H: float64(p.Height)}
}

func safeRect(p scene.Page) geom.Rect {
	m := p.SafeMargin
	return geom.Rect{X: m, Y: m, W: float64(p.Width) - 2*m, H: float64(p.Height) - 2*m}
}

// RenderPage composites one page from its planned draws. Sources are parallel
// to draws and have already been pinned to exact plate revisions by the caller;
// later draws paint over earlier ones.
func RenderPage(ctx context.Context, p scene.Page, draws []scene.Draw, srcs []Source) (PageResult, error) {
	if len(srcs) != len(draws) {
		return PageResult{}, fmt.Errorf("page requires one resolved source per draw")
	}
	out := &raster.Canvas{Width: p.Width, Height: p.Height, Pix: make([]paint.Pixel, p.Width*p.Height)}
	background := paint.From(p.Background)
	for i := range out.Pix {
		out.Pix[i] = background
	}
	res := PageResult{Width: p.Width, Height: p.Height, SafeMargin: p.SafeMargin, Painter: "prism-plate-page-v1"}
	trim := trimBox(p)
	safe := safeRect(p)
	for i, d := range draws {
		pl, src := d.Placement, srcs[i]
		m := scene.SpreadMatrix(pl, p.Width, d.Slice, src.Width, src.Height)
		inv, ok := m.Inverse()
		if !ok {
			return res, fmt.Errorf("page draw %d has a singular transform", i+1)
		}
		bounds := geom.BoundsOf(scene.DrawCorners(pl, p.Width, d.Slice)...)
		total := float64(d.Total)
		sx, sy := scene.FitScale(pl.Fit, src.Width, src.Height, pl.Width*total, pl.Height)
		rep := PlacementReport{Index: i + 1, Pin: src.Pin, Fit: pl.Fit, Bounds: bounds, ScaleX: sx, ScaleY: sy}
		rep.Role, rep.AnchorPage, rep.Slice, rep.Span = d.Role(), d.Anchor+1, d.Slice+1, d.Total
		// Content is the fitted source rectangle in spread coordinates, shifted
		// into this page's coordinates for reporting.
		shift := float64(d.Slice) * float64(p.Width)
		rep.Content = geom.Rect{
			X: pl.X + (pl.Width*total-float64(src.Width)*sx)/2 - shift,
			Y: pl.Y + (pl.Height-float64(src.Height)*sy)/2,
			W: float64(src.Width) * sx,
			H: float64(src.Height) * sy,
		}
		// Letterbox is the part of the placement's whole box that contain leaves
		// empty. It is a property of the placement, reported on every draw.
		boxArea := pl.Width * total * pl.Height
		if pl.Fit == "contain" && rep.Content.W*rep.Content.H < boxArea {
			rep.Letterbox = boxArea - rep.Content.W*rep.Content.H
		}
		rep.Overflow = geom.Overflow(bounds, trim)
		rep.ClippedArea = bounds.Area() - bounds.Intersect(trim).Area()
		rep.ExceedsSafeArea = !safe.ContainsRect(bounds)
		rep.OffPage = bounds.Intersect(trim).Area() <= 0
		if rep.ExceedsSafeArea {
			res.SafeViolate = true
		}
		res.Placements = append(res.Placements, rep)

		clip := bounds.Intersect(trim)
		if clip.W <= 0 || clip.H <= 0 {
			continue
		}
		// Minified sources average up to 4x4 sub-samples to avoid aliasing;
		// magnified sources need only one bilinear tap per output pixel.
		k := int(math.Ceil(math.Max(sx, sy)))
		if k < 1 {
			k = 1
		}
		if k > 4 {
			k = 4
		}
		x0, y0 := int(math.Floor(clip.X)), int(math.Floor(clip.Y))
		x1, y1 := int(math.Ceil(clip.X+clip.W)), int(math.Ceil(clip.Y+clip.H))
		for y := y0; y < y1; y++ {
			if err := ctx.Err(); err != nil {
				return res, err
			}
			if y < 0 || y >= p.Height {
				continue
			}
			for x := x0; x < x1; x++ {
				if x < 0 || x >= p.Width {
					continue
				}
				acc := paint.Pixel{}
				hits := 0
				for sy2 := 0; sy2 < k; sy2++ {
					for sx2 := 0; sx2 < k; sx2++ {
						px := float64(x) + (float64(sx2)+0.5)/float64(k)
						py := float64(y) + (float64(sy2)+0.5)/float64(k)
						q := inv.Apply(geom.Point{X: px, Y: py})
						if q.X < 0 || q.Y < 0 || q.X >= float64(src.Width) || q.Y >= float64(src.Height) {
							continue
						}
						s := src.Canvas.SampleBilinear(q.X, q.Y)
						acc.R += s.R
						acc.G += s.G
						acc.B += s.B
						acc.A += s.A
						hits++
					}
				}
				if hits == 0 {
					continue
				}
				avg := paint.Pixel{R: acc.R / float64(hits), G: acc.G / float64(hits), B: acc.B / float64(hits), A: acc.A / float64(hits)}
				out.Pix[y*p.Width+x] = paint.Over(out.Pix[y*p.Width+x], avg, "normal")
			}
		}
	}
	data, err := raster.EncodePNG(out)
	if err != nil {
		return res, err
	}
	res.PNG = data
	res.Digest = repository.Hash(data)
	return res, nil
}
