package session

import (
	"testing"

	"crossscreen/core/layout"
)

func testRouterLayout() *layout.Layout {
	return &layout.Layout{Version: 1, Screens: []*layout.Screen{
		{ID: "mac:0", Device: "mac", X: 0, Y: 0, Width: 1920, Height: 1080, Primary: true},
		{ID: "win:0", Device: "win", X: 1920, Y: 0, Width: 1280, Height: 800},
	}}
}

func TestRouterStaysOnLocal(t *testing.T) {
	r := NewRouter(testRouterLayout(), "mac")
	mac := r.layout.Screens[0]

	r.OnLocalMove(mac, 100, 100)
	if !r.OnLocal() || r.OnPeer() {
		t.Fatalf("want on local, active=%v", r.Active())
	}
	if r.Active() == nil || r.Active().ID != "mac:0" {
		t.Fatalf("want active mac:0, got %v", r.Active())
	}
}

func TestRouterCrossesToPeer(t *testing.T) {
	r := NewRouter(testRouterLayout(), "mac")
	mac := r.layout.Screens[0]

	r.OnLocalMove(mac, 1919, 400)
	cur := r.OnLocalMove(mac, 1921, 400)
	if cur == nil || cur.ID != "win:0" {
		t.Fatalf("want active win:0, got %v", cur)
	}
	if !r.OnPeer() || r.OnLocal() {
		t.Fatal("want on peer")
	}
	vx, vy := r.Position()
	if vx != 1921 || vy != 400 {
		t.Fatalf("virtual pos should be preserved: (%d,%d)", vx, vy)
	}
}

func TestRouterCrossesBackToLocal(t *testing.T) {
	r := NewRouter(testRouterLayout(), "mac")
	mac := r.layout.Screens[0]

	r.OnLocalMove(mac, 1919, 400)
	r.OnLocalMove(mac, 1921, 400) // onto win
	if !r.OnPeer() {
		t.Fatal("precondition: cursor should be on peer")
	}
	// Local cursor grabbed at the edge; user pulls it back left.
	cur := r.OnLocalMove(mac, 1919, 400)
	if !r.OnLocal() || cur.ID != "mac:0" {
		t.Fatalf("want back on local, active=%v", r.Active())
	}
}

func TestRouterGapClamps(t *testing.T) {
	l := &layout.Layout{Screens: []*layout.Screen{
		{ID: "a:0", Device: "a", X: 0, Y: 0, Width: 100, Height: 100},
		{ID: "b:0", Device: "b", X: 200, Y: 0, Width: 100, Height: 100},
	}}
	r := NewRouter(l, "a")
	a := l.Screens[0]
	r.OnLocalMove(a, 99, 50)
	r.OnLocalMove(a, 150, 50) // gap at 100..199
	if !r.OnLocal() {
		t.Fatalf("want clamped on local, active=%v", r.Active())
	}
	vx, _ := r.Position()
	if vx != 99 {
		t.Fatalf("want clamped x=99, got %d", vx)
	}
}
