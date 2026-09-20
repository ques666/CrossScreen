// Command genicons renders the CrossScreen brand icon set:
//   - app master + .icns (macOS) and multi-size .ico (Windows / favicon)
//   - status-bar template glyph (monochrome, auto-adapts to the menu bar)
//   - status-bar color icon (Windows tray)
//   - web favicon
//
// Brand mark: two adjacent screen panels with a neon cursor arrow crossing
// the seam between them ("跨屏"). The artwork is supersampled and generated
// with only the Go standard library so the brand stays reproducible.
//
// Run from the repository root: go run ./tools/genicons
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
)

// ---------- scene configuration ----------

const grid = 1024.0

// appScene positions on the 1024 master grid.
type appScene struct {
	tileR  float64
	panels [2]rect
	arrow  []xy
}

type rect struct{ x0, y0, x1, y1, r float64 }

type xy struct{ x, y float64 }

// arrowNorm is the classic northwest pointer on a 32-unit grid.
var arrowNorm = []xy{
	{9, 6}, {9, 25}, {13, 21}, {16.5, 27}, {19.5, 25.4}, {16, 19.5}, {23, 19.5},
}

func scalePts(pts []xy, s, tx, ty float64) []xy {
	out := make([]xy, len(pts))
	for i, p := range pts {
		out[i] = xy{p.x*s + tx, p.y*s + ty}
	}
	return out
}

func defaultAppScene() appScene {
	return appScene{
		tileR: 226,
		panels: [2]rect{
			{152, 236, 484, 712, 52},
			{540, 236, 872, 712, 52},
		},
		// Tip on the left panel, tail reaching onto the right one, so the
		// pointer visibly crosses the seam at x=512.
		arrow: scalePts(arrowNorm, 14, 320, 264),
	}
}

// trayScene positions on a 64-unit canvas used for status-bar icons.
type trayScene struct {
	panels [2]rect
	arrow  []xy
}

// trayArrow is the classic northwest pointer with the small tail leg
// omitted: at menu-bar size the 5-point version stays a clean triangle.
var trayArrow = []xy{{9, 6}, {9, 25}, {13, 21}, {16, 19.5}, {23, 19.5}}

func defaultTrayScene() trayScene {
	return trayScene{
		// Back display peeking behind the front one (layered multi-device
		// iconography), sized for an 18pt menu-bar glyph.
		panels: [2]rect{
			{8, 9, 42, 43, 3.5},
			{22, 21, 56, 55, 3.5},
		},
		// Pointer at the front screen's top corner, clear of the frames.
		arrow: scalePts(trayArrow, 0.95, 27.45, 1.3),
	}
}

// scaleTrayScene enlarges a tray scene by k around the 64-canvas center.
func scaleTrayScene(s trayScene, k float64) trayScene {
	ct := func(v float64) float64 { return 32 + (v-32)*k }
	var ps [2]rect
	for i, p := range s.panels {
		ps[i] = rect{ct(p.x0), ct(p.y0), ct(p.x1), ct(p.y1), p.r * k}
	}
	var as []xy
	for _, p := range s.arrow {
		as = append(as, xy{ct(p.x), ct(p.y)})
	}
	return trayScene{panels: ps, arrow: as}
}

// ---------- palette ----------

type rgba struct{ r, g, b, a float64 }

func hex(s string) rgba {
	var r, g, b int
	fmt.Sscanf(s, "#%02x%02x%02x", &r, &g, &b)
	return rgba{float64(r) / 255, float64(g) / 255, float64(b) / 255, 1}
}

func with(c rgba, a float64) rgba { c.a = a; return c }

var (
	cTileTop    = hex("#1b2027")
	cTileBottom = hex("#0a0d10")
	cPanelTop   = hex("#161d25")
	cPanelBot   = hex("#0d1218")
	cPanelEdge  = with(hex("#9caebf"), 0.30)
	cGrid       = with(hex("#3ce08f"), 0.10)
	cArrowTop   = hex("#3df796")
	cArrowBot   = hex("#18d278")
	cArrowEdge  = with(hex("#070a08"), 0.92)
	cSheen      = with(hex("#ffffff"), 0.07)
)

// ---------- supersampled alpha masks ----------

type mask struct {
	w, h int
	a    []float64 // 0..1 coverage
}

func newMask(w, h int) *mask { return &mask{w: w, h: h, a: make([]float64, w*h)} }

func (m *mask) idx(x, y int) int { return y*m.w + x }

func fillRoundedRect(m *mask, b rect) {
	for y := 0; y < m.h; y++ {
		fy := (float64(y) + 0.5) / float64(m.h) * grid
		for x := 0; x < m.w; x++ {
			fx := (float64(x) + 0.5) / float64(m.w) * grid
			if inRoundedRect(fx, fy, b) {
				m.a[m.idx(x, y)] = 1
			}
		}
	}
}

func strokeRoundedRect(m *mask, b rect, sw float64) {
	inner := rect{b.x0 + sw, b.y0 + sw, b.x1 - sw, b.y1 - sw, maxF(b.r-sw, 0)}
	for y := 0; y < m.h; y++ {
		fy := (float64(y) + 0.5) / float64(m.h) * grid
		for x := 0; x < m.w; x++ {
			fx := (float64(x) + 0.5) / float64(m.w) * grid
			if inRoundedRect(fx, fy, b) && !inRoundedRect(fx, fy, inner) {
				m.a[m.idx(x, y)] = 1
			}
		}
	}
}

func fillPolygon(m *mask, pts []xy) {
	minY, maxY := pts[0].y, pts[0].y
	for _, p := range pts {
		minY = math.Min(minY, p.y)
		maxY = math.Max(maxY, p.y)
	}
	for y := 0; y < m.h; y++ {
		yc := (float64(y) + 0.5) / float64(m.h) * grid
		if yc < minY || yc > maxY {
			continue
		}
		var xs []float64
		for i := range pts {
			a, b := pts[i], pts[(i+1)%len(pts)]
			if (a.y > yc) != (b.y > yc) {
				t := (yc - a.y) / (b.y - a.y)
				xs = append(xs, a.x+t*(b.x-a.x))
			}
		}
		for i := 0; i+1 < len(xs); i += 2 {
			lo, hi := xs[i], xs[i+1]
			if lo > hi {
				lo, hi = hi, lo
			}
			x0 := int(math.Ceil(lo/grid*float64(m.w))) - 1
			x1 := int(hi / grid * float64(m.w))
			for x := maxI(x0, 0); x < minI(x1, m.w); x++ {
				m.a[m.idx(x, y)] = 1
			}
		}
	}
}

// radialGlow fills a soft circular light, brightest at (cx,cy) in grid units.
func radialGlow(m *mask, cx, cy, radius, peak float64) {
	for y := 0; y < m.h; y++ {
		fy := (float64(y) + 0.5) / float64(m.h) * grid
		for x := 0; x < m.w; x++ {
			fx := (float64(x) + 0.5) / float64(m.w) * grid
			d := math.Hypot(fx-cx, fy-cy) / radius
			if d < 1 {
				v := (1 - d)
				v = v * v
				m.a[m.idx(x, y)] = peak * v
			}
		}
	}
}

// blur applies a separable box blur (3 passes approximate a gaussian).
func blur(src *mask, radiusPx float64) *mask {
	r := int(math.Round(radiusPx))
	if r <= 0 {
		return src
	}
	cur := src
	tmp := newMask(src.w, src.h)
	for pass := 0; pass < 3; pass++ {
		boxH(cur, tmp, r)
		boxV(tmp, cur, r)
	}
	return cur
}

func boxH(src, dst *mask, r int) {
	for y := 0; y < src.h; y++ {
		var sum float64
		for x := -r; x <= r; x++ {
			sum += src.a[src.idx(clampI(x, 0, src.w-1), y)]
		}
		for x := 0; x < src.w; x++ {
			dst.a[dst.idx(x, y)] = sum / float64(2*r+1)
			out := clampI(x-r, 0, src.w-1)
			in := clampI(x+r+1, 0, src.w-1)
			sum += src.a[src.idx(in, y)] - src.a[src.idx(out, y)]
		}
	}
}

func boxV(src, dst *mask, r int) {
	for x := 0; x < src.w; x++ {
		var sum float64
		for y := -r; y <= r; y++ {
			sum += src.a[src.idx(x, clampI(y, 0, src.h-1))]
		}
		for y := 0; y < src.h; y++ {
			dst.a[dst.idx(x, y)] = sum / float64(2*r+1)
			out := clampI(y-r, 0, src.h-1)
			in := clampI(y+r+1, 0, src.h-1)
			sum += src.a[src.idx(x, in)] - src.a[src.idx(x, out)]
		}
	}
}

// threshold hardens a mask (used to dilate shapes via blur+threshold).
func threshold(m *mask, t float64) {
	for i, v := range m.a {
		if v >= t {
			m.a[i] = 1
		} else {
			m.a[i] = 0
		}
	}
}

// sharpen applies a smoothstep contrast curve to coverage, tightening soft
// edges after downsampling (used for small monochrome glyphs).
func sharpen(m *mask, e0, e1 float64) {
	for i, v := range m.a {
		if v <= e0 {
			m.a[i] = 0
			continue
		}
		if v >= e1 {
			m.a[i] = 1
			continue
		}
		t := (v - e0) / (e1 - e0)
		m.a[i] = t * t * (3 - 2*t)
	}
}

func intersect(a, b *mask) *mask {
	out := newMask(a.w, a.h)
	for i := range out.a {
		out.a[i] = math.Min(a.a[i], b.a[i])
	}
	return out
}

// ---------- downsampling ----------

// downsample area-averages an SS-scaled mask to final 1x coverage.
func downsample(m *mask, ss int) *mask {
	out := newMask(m.w/ss, m.h/ss)
	for y := 0; y < out.h; y++ {
		for x := 0; x < out.w; x++ {
			var sum float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					sum += m.a[(y*ss+sy)*m.w+(x*ss+sx)]
				}
			}
			out.a[y*out.w+x] = sum / float64(ss*ss)
		}
	}
	return out
}

// ---------- compositing ----------

type layer struct {
	mask *mask
	c    func(x, y int) rgba
	mode blendMode
}

type blendMode int

const (
	blendOver blendMode = iota
	blendAdd
)

func render(w, h int, layers []layer) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for _, l := range layers {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				cov := l.mask.a[y*w+x]
				if cov <= 0 {
					continue
				}
				c := l.c(x, y)
				aa := cov * c.a
				if aa <= 0 {
					continue
				}
				i := img.PixOffset(x, y)
				if l.mode == blendAdd {
					img.Pix[i+0] = addByte(img.Pix[i+0], c.r*aa)
					img.Pix[i+1] = addByte(img.Pix[i+1], c.g*aa)
					img.Pix[i+2] = addByte(img.Pix[i+2], c.b*aa)
					continue
				}
				// Over compositing in premultiplied space (Pix is premultiplied).
				dstA := float64(img.Pix[i+3]) / 255
				outA := aa + dstA*(1-aa)
				img.Pix[i+0] = clampByte((c.r*aa + float64(img.Pix[i+0])/255*(1-aa)) * 255)
				img.Pix[i+1] = clampByte((c.g*aa + float64(img.Pix[i+1])/255*(1-aa)) * 255)
				img.Pix[i+2] = clampByte((c.b*aa + float64(img.Pix[i+2])/255*(1-aa)) * 255)
				img.Pix[i+3] = clampByte(outA * 255)
			}
		}
	}
	return img
}

func solid(c rgba) func(x, y int) rgba { return func(_, _ int) rgba { return c } }

func vGrad(h int, top, bottom rgba) func(x, y int) rgba {
	return func(_, y int) rgba {
		t := float64(y) / float64(h)
		return rgba{
			lerp(top.r, bottom.r, t),
			lerp(top.g, bottom.g, t),
			lerp(top.b, bottom.b, t),
			lerp(top.a, bottom.a, t),
		}
	}
}

// ---------- scene builders ----------

// renderApp renders the full-color app tile at final size px.
func renderApp(size int) *image.RGBA {
	ss := 3
	if size >= 256 {
		ss = 2
	}
	S := size * ss
	sc := defaultAppScene()

	// Detail budget: tiny icons reduce to the single strong symbol — a neon
	// pointer on the tile. Mid sizes keep both screens but drop the grid and
	// use heavier strokes/tighter glow.
	simple := size < 48
	showGrid := !simple && size >= 64
	edgeW := 7.0
	if !simple && size < 64 {
		edgeW = 15
	}
	ambientPeak := 0.55
	arrowGlowR := 26.0
	if simple {
		ambientPeak = 0.38
		arrowGlowR = 20
	} else if size < 64 {
		ambientPeak = 0.32
		arrowGlowR = 14
	}
	outlineR := 5.0
	if simple {
		outlineR = 9
	} else if size < 64 {
		outlineR = 8
	}
	arrowPts := sc.arrow
	if simple {
		// Larger, optically centered pointer.
		arrowPts = scalePts(arrowNorm, 15, 313, 210)
	}

	// Tile base.
	tileSS := newMask(S, S)
	fillRoundedRect(tileSS, rect{0, 0, grid, grid, sc.tileR})
	tile := downsample(tileSS, ss)

	// Ambient neon glow behind the panels.
	glowSS := newMask(S, S)
	radialGlow(glowSS, 512, 430, 470, ambientPeak)
	glowSS = intersect(glowSS, tileSS)
	glowSS = blur(glowSS, float64(10*ss))
	glow := downsample(glowSS, ss)

	// Top sheen band.
	sheenSS := newMask(S, S)
	fillRoundedRect(sheenSS, rect{0, 0, grid, grid, sc.tileR})
	sheenGrad := newMask(S, S)
	for y := 0; y < S; y++ {
		gy := float64(y) / float64(S)
		if gy < 0.24 {
			v := (1 - gy/0.24) * 1
			for x := 0; x < S; x++ {
				sheenGrad.a[y*S+x] = v
			}
		}
	}
	sheenSS = intersect(sheenSS, sheenGrad)
	sheen := downsample(sheenSS, ss)

	// Panels (fill + hairline edge + faint internal grid), omitted in the
	// simplified small-size mark.
	var panelFill, panelEdge, panelGrid *mask
	if !simple {
		for _, p := range sc.panels {
			f := newMask(S, S)
			fillRoundedRect(f, p)
			if panelFill == nil {
				panelFill = f
			} else {
				panelFill = union(panelFill, f)
			}

			e := newMask(S, S)
			strokeRoundedRect(e, p, edgeW)
			panelEdge = union(panelEdge, e)

			if showGrid {
				gm := newMask(S, S)
				// faint grid hairlines inside each screen
				for _, frac := range []float64{1 / 3.0, 2 / 3.0} {
					yy := p.y0 + (p.y1-p.y0)*frac
					gline := newMask(S, S)
					fillRoundedRect(gline, rect{p.x0 + 18, yy - 3, p.x1 - 18, yy + 3, 3})
					gm = union(gm, gline)
					vline := newMask(S, S)
					xx := p.x0 + (p.x1-p.x0)*frac
					fillRoundedRect(vline, rect{xx - 3, p.y0 + 18, xx + 3, p.y1 - 18, 3})
					gm = union(gm, vline)
				}
				inner := rect{p.x0 + 14, p.y0 + 14, p.x1 - 14, p.y1 - 14, maxF(p.r-14, 0)}
				clip := newMask(S, S)
				fillRoundedRect(clip, inner)
				gm = intersect(gm, clip)
				panelGrid = union(panelGrid, gm)
			}
		}
	}
	if !simple {
		panelFill = downsample(panelFill, ss)
		panelEdge = downsample(panelEdge, ss)
		if panelGrid != nil {
			panelGrid = downsample(panelGrid, ss)
		}
	}

	// Arrow: glow, dark outline (dilated), neon fill.
	arrowSS := newMask(S, S)
	fillPolygon(arrowSS, arrowPts)

	arrowGlowSS := blur(copyMask(arrowSS), arrowGlowR*float64(ss))
	arrowGlowSS = intersect(arrowGlowSS, tileSS)
	arrowGlow := downsample(arrowGlowSS, ss)

	outlineSS := blur(copyMask(arrowSS), outlineR*float64(ss))
	threshold(outlineSS, 0.18)
	outlineSS = subtract(outlineSS, arrowSS)
	outline := downsample(outlineSS, ss)
	arrow := downsample(arrowSS, ss)

	layers := []layer{
		{tile, vGrad(size, cTileTop, cTileBottom), blendOver},
		{glow, solid(with(cArrowTop, 0.5)), blendAdd},
	}
	if !simple {
		layers = append(layers, layer{sheen, solid(cSheen), blendOver})
		layers = append(layers, layer{panelFill, func(_, y int) rgba {
			t := float64(y) / float64(size)
			return rgba{lerp(cPanelTop.r, cPanelBot.r, t), lerp(cPanelTop.g, cPanelBot.g, t), lerp(cPanelTop.b, cPanelBot.b, t), 1}
		}, blendOver})
	}
	if panelGrid != nil {
		layers = append(layers, layer{panelGrid, solid(cGrid), blendOver})
	}
	if panelEdge != nil {
		edgeColor := cPanelEdge
		if size < 64 {
			edgeColor = with(hex("#a9b9c8"), 0.42)
		}
		layers = append(layers, layer{panelEdge, solid(edgeColor), blendOver})
	}
	layers = append(layers,
		layer{arrowGlow, solid(with(cArrowTop, 0.55)), blendAdd},
		layer{outline, solid(cArrowEdge), blendOver},
		layer{arrow, func(_, y int) rgba {
			t := clampF(float64(y)/float64(size), 0, 1)
			return rgba{lerp(cArrowTop.r, cArrowBot.r, t), lerp(cArrowTop.g, cArrowBot.g, t), lerp(cArrowTop.b, cArrowBot.b, t), 1}
		}, blendOver},
	)
	return render(size, size, layers)
}

// renderTrayTemplate renders the monochrome macOS menu-bar glyph. The pointer
// is layered in front of the two screens with a hairline knockout gap, the
// standard monochrome way of separating overlapping shapes. Alpha carries
// the shape; macOS recolors the glyph for light/dark menu bars automatically.
func renderTrayTemplate(size int) *image.RGBA {
	ss := 4
	S := size * ss
	sc := scaleTrayScene(defaultTrayScene(), 1.12)

	back, front := sc.panels[0], sc.panels[1]
	backStroke, frontStroke, frontFill := newMask(S, S), newMask(S, S), newMask(S, S)
	strokeRoundedRect64(backStroke, back, 3.6, size)
	strokeRoundedRect64(frontStroke, front, 3.6, size)
	fillRoundedRect64(frontFill, front, size)

	// Front screen occludes the back one, with a hairline separation.
	occluder := dilateMask(copyMask(frontFill), 0.9/64*float64(S))
	screens := subtract(backStroke, occluder)
	screens = union(screens, frontStroke)

	// Pointer sits in front, separated from the frames by the same gap.
	arrow := newMask(S, S)
	fillPolyGrid(arrow, sc.arrow, 64)
	screens = subtract(screens, dilateMask(copyMask(arrow), 1.1/64*float64(S)))

	m := union(screens, arrow)
	m = downsample(m, ss)
	sharpen(m, 0.4, 0.6)
	return render(size, size, []layer{
		{m, solid(rgba{0, 0, 0, 1}), blendOver},
	})
}

// dilateMask grows a binary mask by radius g via blur+threshold.
func dilateMask(m *mask, r float64) *mask {
	if r <= 0 {
		return m
	}
	d := blur(m, r/0.64)
	threshold(d, 0.18)
	return d
}

// renderTrayColor renders the Windows tray icon: compact dark tile with two
// screens and a neon crossing arrow. At 16px the mark collapses to a single
// bold pointer so it survives the taskbar scale.
func renderTrayColor(size int) *image.RGBA {
	ss := 4
	S := size * ss
	sc := scaleTrayScene(defaultTrayScene(), 1.12)
	simple := size < 20

	tileSS := newMask(S, S)
	fillRoundedRect64(tileSS, rect{2, 2, 62, 62, 13}, size)
	tile := downsample(tileSS, ss)

	var fill, edge *mask
	if !simple {
		backP, frontP := sc.panels[0], sc.panels[1]
		bf, ff := newMask(S, S), newMask(S, S)
		fillRoundedRect64(bf, backP, size)
		fillRoundedRect64(ff, frontP, size)
		fill = union(bf, ff)

		frontFillD := dilateMask(copyMask(ff), 0.9/64*float64(S))
		be, fe := newMask(S, S), newMask(S, S)
		strokeRoundedRect64(be, backP, 1.8, size)
		strokeRoundedRect64(fe, frontP, 1.8, size)
		edge = union(subtract(be, frontFillD), fe)

		fill = downsample(fill, ss)
		edge = downsample(edge, ss)
	}

	am := newMask(S, S)
	var colorArrow []xy
	if simple {
		colorArrow = scalePts(arrowNorm, 1.5, 13.5, 7)
	} else {
		// Slightly larger pointer than the template glyph.
		colorArrow = scalePts(sc.arrow, 1.06, -1, 0)
	}
	fillPolyGrid(am, colorArrow, 64)
	arrowGlowSS := blur(copyMask(am), float64(2*ss))
	arrowGlowSS = intersect(arrowGlowSS, tileSS)
	arrowGlow := downsample(arrowGlowSS, ss)

	ol := newMask(S, S)
	outlineW := 2.4
	if simple {
		outlineW = 2.8
	}
	strokePolygonPts64(ol, colorArrow, outlineW, S)
	outline := downsample(ol, ss)
	arrow := downsample(am, ss)

	layers := []layer{{tile, solid(hex("#11151a")), blendOver}}
	if fill != nil {
		layers = append(layers, layer{fill, solid(hex("#0b0f13")), blendOver})
	}
	if edge != nil {
		layers = append(layers, layer{edge, solid(with(hex("#9caebf"), 0.4)), blendOver})
	}
	layers = append(layers,
		layer{arrowGlow, solid(with(cArrowTop, 0.6)), blendAdd},
		layer{outline, solid(cArrowEdge), blendOver},
		layer{arrow, solid(cArrowTop), blendOver},
	)
	return render(size, size, layers)
}

// 64-unit grid variants (tray canvas is 64 instead of 1024).
func fillRoundedRect64(m *mask, b rect, size int) {
	fillGrid(m, 64, func(fx, fy float64) bool { return inRoundedRect(fx, fy, b) })
}

func strokeRoundedRect64(m *mask, b rect, sw float64, size int) {
	inner := rect{b.x0 + sw, b.y0 + sw, b.x1 - sw, b.y1 - sw, maxF(b.r-sw, 0)}
	fillGrid(m, 64, func(fx, fy float64) bool {
		return inRoundedRect(fx, fy, b) && !inRoundedRect(fx, fy, inner)
	})
}

func fillPolygon64(m *mask, pts []xy, size int) {
	fillPolyGrid(m, pts, 64)
}

// strokePolygonPts64 produces an outline of width sw (64-grid units) for a
// polygon rendered into a mask of width S px.
func strokePolygonPts64(m *mask, pts []xy, sw float64, S int) {
	fill := newMask(S, S)
	fillPolyGrid(fill, pts, 64)
	d := blur(fill, sw/64*float64(S))
	threshold(d, 0.25)
	m.a = subtract(d, fill).a
}

func fillGrid(m *mask, g float64, hit func(x, y float64) bool) {
	for y := 0; y < m.h; y++ {
		fy := (float64(y) + 0.5) / float64(m.h) * g
		for x := 0; x < m.w; x++ {
			fx := (float64(x) + 0.5) / float64(m.w) * g
			if hit(fx, fy) {
				m.a[y*m.w+x] = 1
			}
		}
	}
}

func fillPolyGrid(m *mask, pts []xy, g float64) {
	minY, maxY := pts[0].y, pts[0].y
	for _, p := range pts {
		minY = math.Min(minY, p.y)
		maxY = math.Max(maxY, p.y)
	}
	for y := 0; y < m.h; y++ {
		yc := (float64(y) + 0.5) / float64(m.h) * g
		if yc < minY || yc > maxY {
			continue
		}
		var xs []float64
		for i := range pts {
			a, b := pts[i], pts[(i+1)%len(pts)]
			if (a.y > yc) != (b.y > yc) {
				t := (yc - a.y) / (b.y - a.y)
				xs = append(xs, a.x+t*(b.x-a.x))
			}
		}
		for i := 0; i+1 < len(xs); i += 2 {
			lo, hi := xs[i], xs[i+1]
			if lo > hi {
				lo, hi = hi, lo
			}
			x0 := int(math.Ceil(lo/g*float64(m.w))) - 1
			x1 := int(hi / g * float64(m.w))
			for x := maxI(x0, 0); x < minI(x1, m.w); x++ {
				m.a[y*m.w+x] = 1
			}
		}
	}
}

// ---------- mask algebra ----------

func union(a, b *mask) *mask {
	if a == nil {
		return b
	}
	out := newMask(a.w, a.h)
	for i := range out.a {
		out.a[i] = math.Max(a.a[i], b.a[i])
	}
	return out
}

func subtract(a, b *mask) *mask {
	out := newMask(a.w, a.h)
	for i := range out.a {
		out.a[i] = clampF(a.a[i]-b.a[i], 0, 1)
	}
	return out
}

func copyMask(m *mask) *mask {
	c := newMask(m.w, m.h)
	copy(c.a, m.a)
	return c
}

// ---------- geometry helpers ----------

func inRoundedRect(x, y float64, b rect) bool {
	if x < b.x0 || x > b.x1 || y < b.y0 || y > b.y1 {
		return false
	}
	cx := clampF(x, b.x0+b.r, b.x1-b.r)
	cy := clampF(y, b.y0+b.r, b.y1-b.r)
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= b.r*b.r
}

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
func clampI(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
func maxI(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func minI(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func lerp(a, b, t float64) float64 { return a + (b-a)*t }

func clampByte(v float64) uint8 {
	v = math.Round(v)
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

func addByte(cur uint8, add float64) uint8 {
	return clampByte(float64(cur) + add*255)
}

// ---------- encoders ----------

func writePNG(path string, img image.Image) error {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// icoEntry is one image inside an .ico container.
type icoEntry struct {
	img     *image.RGBA
	pngMode bool // Vista+: PNG-compressed (used for large sizes)
}

// encodeICO builds a multi-size Windows .ico. Sizes <=48 use the classic
// BMP payload; larger sizes embed PNGs.
func encodeICO(entries []icoEntry) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, uint16(0))
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))
	_ = binary.Write(&b, binary.LittleEndian, uint16(len(entries)))

	var payloads [][]byte
	headerSize := 6 + 16*len(entries)
	offset := headerSize
	for _, e := range entries {
		var data []byte
		if e.pngMode {
			var pb bytes.Buffer
			_ = png.Encode(&pb, e.img)
			data = pb.Bytes()
		} else {
			data = encodeDIB(e.img)
		}
		payloads = append(payloads, data)

		w := e.img.Bounds().Dx()
		h := e.img.Bounds().Dy()
		b.WriteByte(byte(w % 256)) // 0 encodes 256
		b.WriteByte(byte(h % 256))
		b.WriteByte(0)
		b.WriteByte(0)
		_ = binary.Write(&b, binary.LittleEndian, uint16(1))
		_ = binary.Write(&b, binary.LittleEndian, uint16(32))
		_ = binary.Write(&b, binary.LittleEndian, uint32(len(data)))
		_ = binary.Write(&b, binary.LittleEndian, uint32(offset))
		offset += len(data)
	}
	for _, p := range payloads {
		b.Write(p)
	}
	return b.Bytes()
}

// encodeDIB packs an RGBA image into a 32-bit BMP (BITMAPINFOHEADER + BGRA
// bottom-up XOR mask + AND mask), the classic .ico payload format.
func encodeDIB(img *image.RGBA) []byte {
	w := img.Bounds().Dx()
	h := img.Bounds().Dy()
	bihSize := 40
	andStride := (w + 7) / 8
	andSize := andStride * h
	xorSize := w * h * 4

	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, uint32(bihSize))
	_ = binary.Write(&b, binary.LittleEndian, int32(w))
	_ = binary.Write(&b, binary.LittleEndian, int32(h*2)) // XOR + AND
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))
	_ = binary.Write(&b, binary.LittleEndian, uint16(32))
	_ = binary.Write(&b, binary.LittleEndian, uint32(0))
	_ = binary.Write(&b, binary.LittleEndian, uint32(xorSize+andSize))
	_ = binary.Write(&b, binary.LittleEndian, uint32(0))
	_ = binary.Write(&b, binary.LittleEndian, uint32(0))
	_ = binary.Write(&b, binary.LittleEndian, uint32(0))
	_ = binary.Write(&b, binary.LittleEndian, uint32(0))
	_ = binary.Write(&b, binary.LittleEndian, uint32(0))
	for y := h - 1; y >= 0; y-- {
		for x := 0; x < w; x++ {
			i := img.PixOffset(x, y)
			b.WriteByte(img.Pix[i+2])
			b.WriteByte(img.Pix[i+1])
			b.WriteByte(img.Pix[i+0])
			b.WriteByte(img.Pix[i+3])
		}
	}
	b.Write(make([]byte, andSize))
	return b.Bytes()
}

// ---------- output pipeline ----------

func main() {
	mustMkdir("assets")
	mustMkdir("platform/tray/assets")
	mustMkdir("platform/control/web")
	mustMkdir("cmd/crossscreen/winres")

	// 1. App master + .icns.
	master := renderApp(1024)
	must(writePNG("assets/AppIcon-1024.png", master))
	must(buildICNS("assets/AppIcon.iconset", "assets/AppIcon.icns"))

	// 2. Windows application icon source: go-winres derives all sizes from
	// the 256px PNG in cmd/crossscreen/winres.
	must(writePNG("cmd/crossscreen/winres/icon.png", renderApp(256)))

	// 3. Status-bar assets.
	must(writePNG("platform/tray/assets/tray-template.png", renderTrayTemplate(64)))
	trayICO := encodeICO([]icoEntry{
		{renderTrayColor(16), false},
		{renderTrayColor(32), false},
	})
	must(os.WriteFile("platform/tray/assets/tray.ico", trayICO, 0o644))

	// 4. Web favicon.
	must(writePNG("platform/control/web/favicon.png", renderApp(64)))

	fmt.Println("icons generated")
}

// previewOn composites an icon onto an opaque background, optionally forcing
// the glyph to white (recolor=-1 simulates macOS dark menu-bar templates),
// then nearest-neighbor upscales by k for pixel-accurate inspection.
func previewOn(src *image.RGBA, bg rgba, k int, recolor int) *image.RGBA {
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	out := image.NewRGBA(image.Rect(0, 0, w*k, h*k))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := src.PixOffset(x, y)
			a := float64(src.Pix[i+3]) / 255
			// Pix is premultiplied.
			r := float64(src.Pix[i+0]) / 255
			g := float64(src.Pix[i+1]) / 255
			b := float64(src.Pix[i+2]) / 255
			if recolor == -1 {
				r, g, b = a, a, a // white template, intensity from alpha
				a = 1
			} else if recolor == 1 {
				r, g, b = 0, 0, 0
			}
			fr := r + bg.r*(1-a)
			fg := g + bg.g*(1-a)
			fb := b + bg.b*(1-a)
			for dy := 0; dy < k; dy++ {
				for dx := 0; dx < k; dx++ {
					j := out.PixOffset(x*k+dx, y*k+dy)
					out.Pix[j+0] = clampByte(fr * 255)
					out.Pix[j+1] = clampByte(fg * 255)
					out.Pix[j+2] = clampByte(fb * 255)
					out.Pix[j+3] = 255
				}
			}
		}
	}
	return out
}

func mustMkdir(p string) { must(os.MkdirAll(p, 0o755)) }

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "genicons:", err)
		os.Exit(1)
	}
}

// buildICNS renders all iconset sizes and converts via macOS iconutil.
func buildICNS(iconset, outICNS string) error {
	must(os.MkdirAll(iconset, 0o755))
	type spec struct {
		name string
		size int
	}
	specs := []spec{
		{"icon_16x16.png", 16},
		{"icon_16x16@2x.png", 32},
		{"icon_32x32.png", 32},
		{"icon_32x32@2x.png", 64},
		{"icon_128x128.png", 128},
		{"icon_128x128@2x.png", 256},
		{"icon_256x256.png", 256},
		{"icon_256x256@2x.png", 512},
		{"icon_512x512.png", 512},
		{"icon_512x512@2x.png", 1024},
	}
	for _, s := range specs {
		if err := writePNG(filepath.Join(iconset, s.name), renderApp(s.size)); err != nil {
			return err
		}
	}
	_ = os.Remove(outICNS)
	cmd := exec.Command("iconutil", "-c", "icns", iconset, "-o", outICNS)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("iconutil: %v: %s", err, out)
	}
	return nil
}
