package geom

import "math"

// Rect is an axis-aligned rectangle in page coordinates.
type Rect struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

func (r Rect) Area() float64 {
	if r.W <= 0 || r.H <= 0 {
		return 0
	}
	return r.W * r.H
}

// Intersect returns the overlapping rectangle, or the zero rectangle.
func (r Rect) Intersect(o Rect) Rect {
	x0, y0 := math.Max(r.X, o.X), math.Max(r.Y, o.Y)
	x1, y1 := math.Min(r.X+r.W, o.X+o.W), math.Min(r.Y+r.H, o.Y+o.H)
	if x1 <= x0 || y1 <= y0 {
		return Rect{}
	}
	return Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

func (r Rect) ContainsRect(o Rect) bool {
	return o.X >= r.X-1e-9 && o.Y >= r.Y-1e-9 && o.X+o.W <= r.X+r.W+1e-9 && o.Y+o.H <= r.Y+r.H+1e-9
}

// Edges reports how far a rectangle extends past each side of a boundary.
// Zero means inside or exactly flush.
type Edges struct {
	Left   float64 `json:"left"`
	Top    float64 `json:"top"`
	Right  float64 `json:"right"`
	Bottom float64 `json:"bottom"`
}

func (e Edges) Any() bool { return e.Left > 0 || e.Top > 0 || e.Right > 0 || e.Bottom > 0 }

func Overflow(inner, boundary Rect) Edges {
	return Edges{
		Left:   math.Max(0, boundary.X-inner.X),
		Top:    math.Max(0, boundary.Y-inner.Y),
		Right:  math.Max(0, inner.X+inner.W-(boundary.X+boundary.W)),
		Bottom: math.Max(0, inner.Y+inner.H-(boundary.Y+boundary.H)),
	}
}

func BoundsOf(points ...Point) Rect {
	if len(points) == 0 {
		return Rect{}
	}
	minX, minY := points[0].X, points[0].Y
	maxX, maxY := minX, minY
	for _, p := range points[1:] {
		minX, minY = math.Min(minX, p.X), math.Min(minY, p.Y)
		maxX, maxY = math.Max(maxX, p.X), math.Max(maxY, p.Y)
	}
	return Rect{X: minX, Y: minY, W: maxX - minX, H: maxY - minY}
}
