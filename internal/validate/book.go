package validate

import (
	"fmt"
	"math"
	"prismplate/internal/scene"
)

const (
	MaxPageDimension = 1024
	MaxPages         = 32
	MaxBookRevisions = 64
	MaxPlacements    = 16
	MaxPlacementBox  = 16384.0
	MaxBookWork      = 64_000_000 // bounded source samples per book revision
	MaxDrawsPerPage  = 64
	MaxSources       = 32
	// MaxSourcePixels bounds distinct source canvases held for one composition.
	// Each canvas is one linear-light sample per source pixel.
	MaxSourcePixels = 4 << 20
)

// Page admits one proof page. Placement boxes and rotations are explicit; the
// safe margin is a checked constraint, never an automatic nudge.
func Page(p scene.Page) error {
	if p.Width < 1 || p.Width > MaxPageDimension || p.Height < 1 || p.Height > MaxPageDimension {
		return fmt.Errorf("invalid page size (maximum %d square)", MaxPageDimension)
	}
	if !Color(p.Background) {
		return fmt.Errorf("invalid page background")
	}
	if !finite(p.SafeMargin) || p.SafeMargin < 0 || 2*p.SafeMargin >= math.Min(float64(p.Width), float64(p.Height)) {
		return fmt.Errorf("invalid page safe margin")
	}
	if len(p.Placements) > MaxPlacements {
		return fmt.Errorf("page requires 0..%d placements", MaxPlacements)
	}
	for i, pl := range p.Placements {
		if !ID(pl.Plate) || pl.Revision < 1 {
			return fmt.Errorf("placement %d requires a plate and positive revision", i+1)
		}
		for _, v := range []float64{pl.X, pl.Y, pl.Width, pl.Height} {
			if !finite(v) {
				return fmt.Errorf("placement %d has non-finite box", i+1)
			}
		}
		if pl.Width <= 0 || pl.Height <= 0 || pl.Width > MaxPlacementBox || pl.Height > MaxPlacementBox {
			return fmt.Errorf("placement %d box must be positive and bounded", i+1)
		}
		if !finite(pl.Rotation) || math.Abs(pl.Rotation) > 3600 {
			return fmt.Errorf("placement %d has invalid rotation", i+1)
		}
		if pl.Fit != "contain" && pl.Fit != "cover" && pl.Fit != "stretch" {
			return fmt.Errorf("placement %d has unknown fit mode", i+1)
		}
		if pl.Span < 0 || pl.Span > 16 {
			return fmt.Errorf("placement %d span must be 0..16 pages", i+1)
		}
		if pl.Digest != "" && !digest(pl.Digest) {
			return fmt.Errorf("placement %d has malformed digest", i+1)
		}
	}
	return nil
}

func digest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Sources admits the distinct source set of one book revision. Distinct
// revisions of the same plate count separately because each is rasterized.
func Sources(sizes map[[3]string]int) error {
	if len(sizes) > MaxSources {
		return fmt.Errorf("book references %d distinct sources (maximum %d)", len(sizes), MaxSources)
	}
	total := 0
	for _, n := range sizes {
		total += n
	}
	if total > MaxSourcePixels {
		return fmt.Errorf("book source canvases total %d pixels (maximum %d)", total, MaxSourcePixels)
	}
	return nil
}

// Pages admits an ordered page set and its bounded composition work.
func Pages(pages []scene.Page) error {
	if len(pages) < 1 || len(pages) > MaxPages {
		return fmt.Errorf("book requires 1..%d pages", MaxPages)
	}
	if err := scene.CheckSpans(pages); err != nil {
		return err
	}
	work := int64(0)
	for i, p := range pages {
		if err := Page(p); err != nil {
			return fmt.Errorf("page %d: %w", i+1, err)
		}
		// Cross-page placements contribute inherited draws, so budget the plan.
		draws := len(scene.Plan(pages, i))
		if draws > MaxDrawsPerPage {
			return fmt.Errorf("page %d carries %d draws (maximum %d)", i+1, draws, MaxDrawsPerPage)
		}
		work += int64(p.Width) * int64(p.Height) * int64(draws+1) * 4
	}
	if work > MaxBookWork {
		return fmt.Errorf("book exceeds bounded composition work")
	}
	return nil
}
