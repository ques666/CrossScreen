package layout

// Edge identifies which border of a screen a pointer crosses.
type Edge uint8

const (
	EdgeLeft Edge = iota
	EdgeRight
	EdgeTop
	EdgeBottom
)

// MoveResult describes the outcome of advancing the pointer to (nx, ny).
type MoveResult struct {
	Screen  *Screen // screen the pointer ends on; nil when it left into a gap
	X, Y    int32   // virtual position after the move (clamped on gaps)
	Crossed bool    // true when the pointer switched to a different screen
}

// MovePointer advances a pointer from (x, y) to (nx, ny).
//
// Rules:
//   - Staying inside the current screen keeps the position.
//   - Crossing a shared edge moves the pointer to the adjacent screen,
//     preserving the virtual position (the layout offsets do the mapping).
//   - Moving into a gap (no adjacent screen) clamps to the current border.
//   - A fast jump that lands inside another screen follows it (e.g. after a
//     window switch or a synthetic teleport).
func (l *Layout) MovePointer(x, y, nx, ny int32) MoveResult {
	cur := l.ScreenAt(x, y)
	if cur == nil {
		// Pointer was outside any screen; just re-locate.
		return MoveResult{Screen: l.ScreenAt(nx, ny), X: nx, Y: ny}
	}
	if cur.Contains(nx, ny) {
		return MoveResult{Screen: cur, X: nx, Y: ny}
	}
	if dst, ok := l.acrossEdge(cur, exitEdge(cur, nx, ny), nx, ny); ok {
		return MoveResult{Screen: dst, X: nx, Y: ny, Crossed: true}
	}
	cx, cy := clampTo(cur, nx, ny)
	return MoveResult{Screen: cur, X: cx, Y: cy}
}

func exitEdge(s *Screen, nx, ny int32) Edge {
	switch {
	case nx >= s.X+s.Width:
		return EdgeRight
	case nx < s.X:
		return EdgeLeft
	case ny >= s.Y+s.Height:
		return EdgeBottom
	default:
		return EdgeTop
	}
}

// acrossEdge finds the screen reachable from s across e that contains (nx, ny).
func (l *Layout) acrossEdge(s *Screen, e Edge, nx, ny int32) (*Screen, bool) {
	for _, d := range l.Screens {
		if d.ID == s.ID || !adjacent(s, d) {
			continue
		}
		if d.Contains(nx, ny) {
			return d, true
		}
	}
	if d := l.ScreenAt(nx, ny); d != nil {
		return d, true
	}
	return nil, false
}

// adjacent reports whether d touches s along an edge with overlapping span.
func adjacent(s, d *Screen) bool {
	switch {
	case d.X+d.Width == s.X && spansOverlap(s.Y, s.Y+s.Height-1, d.Y, d.Y+d.Height-1):
		return true // d is to the left of s
	case s.X+s.Width == d.X && spansOverlap(s.Y, s.Y+s.Height-1, d.Y, d.Y+d.Height-1):
		return true // d is to the right of s
	case d.Y+d.Height == s.Y && spansOverlap(s.X, s.X+s.Width-1, d.X, d.X+d.Width-1):
		return true // d is above s
	case s.Y+s.Height == d.Y && spansOverlap(s.X, s.X+s.Width-1, d.X, d.X+d.Width-1):
		return true // d is below s
	}
	return false
}

func spansOverlap(a0, a1, b0, b1 int32) bool {
	return a0 <= b1 && b0 <= a1
}

func clampTo(s *Screen, x, y int32) (int32, int32) {
	if x < s.X {
		x = s.X
	}
	if x >= s.X+s.Width {
		x = s.X + s.Width - 1
	}
	if y < s.Y {
		y = s.Y
	}
	if y >= s.Y+s.Height {
		y = s.Y + s.Height - 1
	}
	return x, y
}
