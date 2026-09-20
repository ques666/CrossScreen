package layout

import "testing"

// testLayout builds a 2x2-ish virtual desktop:
//
//	C (0,-720, 1920x720)   above A
//	A (0,0, 1920x1080)     primary
//	B (1920,200, 1280x800) right of A, vertical offset (partial overlap)
func testLayout() *Layout {
	return &Layout{
		Version: 1,
		Screens: []*Screen{
			{ID: "mac:0", Device: "mac", Name: "Main", X: 0, Y: 0, Width: 1920, Height: 1080, Primary: true},
			{ID: "win:0", Device: "win", Name: "Right", X: 1920, Y: 200, Width: 1280, Height: 800},
			{ID: "mac:1", Device: "mac", Name: "Top", X: 0, Y: -720, Width: 1920, Height: 720},
		},
	}
}

func TestScreenAt(t *testing.T) {
	l := testLayout()
	cases := []struct {
		x, y int32
		id   ScreenID
	}{
		{10, 10, "mac:0"},
		{1919, 1079, "mac:0"},
		{1920, 200, "win:0"},
		{3199, 999, "win:0"},
		{0, -720, "mac:1"},
		{1919, -1, "mac:1"},
		{1920, -1, ""},  // gap between B's top and A/B column
		{2000, 100, ""}, // no screen covers this area (B starts at y=200)
	}
	for _, c := range cases {
		got := l.ScreenAt(c.x, c.y)
		if c.id == "" {
			if got != nil {
				t.Errorf("ScreenAt(%d,%d): want nil, got %s", c.x, c.y, got.ID)
			}
			continue
		}
		if got == nil || got.ID != c.id {
			t.Errorf("ScreenAt(%d,%d): want %s, got %v", c.x, c.y, c.id, got)
		}
	}
}

func TestMoveWithinScreen(t *testing.T) {
	l := testLayout()
	r := l.MovePointer(100, 100, 200, 150)
	if r.Screen == nil || r.Screen.ID != "mac:0" {
		t.Fatalf("want mac:0, got %v", r.Screen)
	}
	if r.X != 200 || r.Y != 150 || r.Crossed {
		t.Fatalf("unexpected result: %+v", r)
	}
}

func TestCrossRightIntoB(t *testing.T) {
	l := testLayout()
	// From A's right edge into B (y=500 is inside B's vertical span).
	r := l.MovePointer(1919, 500, 1921, 500)
	if r.Screen == nil || r.Screen.ID != "win:0" {
		t.Fatalf("want win:0, got %v", r.Screen)
	}
	if !r.Crossed {
		t.Fatal("want crossed=true")
	}
	if r.X != 1921 || r.Y != 500 {
		t.Fatalf("virtual pos should be preserved: %+v", r)
	}
	// Local coords on B.
	if lx, ly := r.Screen.ToLocal(r.X, r.Y); lx != 1 || ly != 300 {
		t.Fatalf("local coords: got (%d,%d), want (1,300)", lx, ly)
	}
}

func TestCrossLeftBackToA(t *testing.T) {
	l := testLayout()
	r := l.MovePointer(1920, 500, 1918, 500)
	if r.Screen == nil || r.Screen.ID != "mac:0" {
		t.Fatalf("want mac:0, got %v", r.Screen)
	}
	if !r.Crossed {
		t.Fatal("want crossed=true")
	}
}

func TestCrossTopToC(t *testing.T) {
	l := testLayout()
	r := l.MovePointer(100, 0, 100, -1)
	if r.Screen == nil || r.Screen.ID != "mac:1" {
		t.Fatalf("want mac:1, got %v", r.Screen)
	}
	if !r.Crossed {
		t.Fatal("want crossed=true")
	}
	if r.Y != -1 {
		t.Fatalf("want y=-1, got %d", r.Y)
	}
}

func TestPartialOverlapYClamps(t *testing.T) {
	l := testLayout()
	// y=100 is above B's span (200..999): pointer must clamp to A's right edge.
	r := l.MovePointer(1919, 100, 1921, 100)
	if r.Screen == nil || r.Screen.ID != "mac:0" {
		t.Fatalf("want mac:0, got %v", r.Screen)
	}
	if r.Crossed {
		t.Fatal("want crossed=false when target y is out of B's span")
	}
	if r.X != 1919 || r.Y != 100 {
		t.Fatalf("want clamped to (1919,100), got %+v", r)
	}
}

func TestGapClamps(t *testing.T) {
	// A at (0,0,100,100), D at (200,0,100,100): 100px gap between them.
	l := &Layout{
		Screens: []*Screen{
			{ID: "a:0", Device: "a", X: 0, Y: 0, Width: 100, Height: 100},
			{ID: "d:0", Device: "d", X: 200, Y: 0, Width: 100, Height: 100},
		},
	}
	r := l.MovePointer(99, 50, 150, 50)
	if r.Screen == nil || r.Screen.ID != "a:0" {
		t.Fatalf("want a:0 (gap), got %v", r.Screen)
	}
	if r.X != 99 || r.Y != 50 {
		t.Fatalf("want clamped to (99,50), got %+v", r)
	}
}

func TestFastJumpAcrossScreens(t *testing.T) {
	// A (0,0,100,100), E (100,0,100,100), F (200,0,100,100).
	l := &Layout{
		Screens: []*Screen{
			{ID: "a:0", Device: "a", X: 0, Y: 0, Width: 100, Height: 100},
			{ID: "e:0", Device: "e", X: 100, Y: 0, Width: 100, Height: 100},
			{ID: "f:0", Device: "f", X: 200, Y: 0, Width: 100, Height: 100},
		},
	}
	// One big step overshoots E and lands in F.
	r := l.MovePointer(95, 50, 250, 50)
	if r.Screen == nil || r.Screen.ID != "f:0" {
		t.Fatalf("want f:0, got %v", r.Screen)
	}
	if !r.Crossed {
		t.Fatal("want crossed=true")
	}
}

func TestToLocal(t *testing.T) {
	s := &Screen{ID: "b:0", Device: "b", X: 1920, Y: 200, Width: 1280, Height: 800}
	if x, y := s.ToLocal(1920, 200); x != 0 || y != 0 {
		t.Fatalf("origin: got (%d,%d)", x, y)
	}
	if x, y := s.ToLocal(3199, 999); x != 1279 || y != 799 {
		t.Fatalf("far corner: got (%d,%d)", x, y)
	}
}
