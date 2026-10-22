package app

// cmd_pcb_pad_entry.go — offline plans for how tracks enter pads (no editor
// writes): neck-down of a track wider than the pad it enters, and line-shaped
// teardrops (two tapered tracks from the pad edge onto the track). EasyEDA's
// native teardrop object has no public eda.* create API (checked 0.4.25 types and
// the live Web 4.1.78 runtime), so the teardrop here is ordinary net-bound copper
// — the "线条形状泪滴" an exam allows. Both read a fresh `pcb dump --include-copper`
// and emit a plan plus an `easyeda apply` playbook of typed pcb.route.delete /
// pcb.line.create steps; run it, then pour-rebuild → save → reload → DRC.

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

type peTrack struct {
	ID     string
	Net    string
	Layer  int
	X1, Y1 float64
	X2, Y2 float64
	W      float64
	Locked bool
}

type pePad struct {
	Ref        string
	Net        string
	Layer      int // 1 top, 2 bottom, 12 multi-layer (through-hole)
	X, Y, W, H float64
	Kind       string // RECT / OVAL (stadium) / ELLIPSE …; W/H are world-aligned
}

type peVia struct {
	Net     string
	X, Y, D float64
}

type peLine struct {
	Net    string     `json:"net"`
	Layer  int        `json:"layer"`
	Width  float64    `json:"width"`
	From   [2]float64 `json:"from"`
	To     [2]float64 `json:"to"`
	Reason string     `json:"reason,omitempty"`
}

type peEdit struct {
	DeleteID string   `json:"deleteTrack"`
	Pad      string   `json:"pad"`
	Net      string   `json:"net"`
	From     float64  `json:"fromWidth"`
	To       float64  `json:"toWidth"`
	Creates  []peLine `json:"creates"`
}

type peSkip struct {
	Track  string `json:"track"`
	Pad    string `json:"pad"`
	Reason string `json:"reason"`
}

func (p pePad) onLayer(layer int) bool { return p.Layer == layer || p.Layer == 12 }

func (p pePad) contains(x, y float64) bool {
	return math.Abs(x-p.X) <= p.W/2+1e-6 && math.Abs(y-p.Y) <= p.H/2+1e-6
}

// insideCopper reports whether (x,y) lies at least `margin` inside the pad's
// real outline: a round J1 pin's bounding-box corner is no copper (live: four
// teardrop lines started outside the 66.9 mil circle and read as dangling ends).
func (p pePad) insideCopper(x, y, margin float64) bool {
	dx, dy := x-p.X, y-p.Y
	switch p.Kind {
	case "ELLIPSE":
		a, b := p.W/2-margin, p.H/2-margin
		return a > 0 && b > 0 && (dx*dx)/(a*a)+(dy*dy)/(b*b) <= 1
	case "OVAL":
		r := math.Min(p.W, p.H) / 2
		half := math.Abs(p.W-p.H) / 2
		if p.W >= p.H {
			return peSegDist(dx, dy, -half, 0, half, 0) <= r-margin
		}
		return peSegDist(dx, dy, 0, -half, 0, half) <= r-margin
	default:
		return math.Abs(dx) <= p.W/2-margin && math.Abs(dy) <= p.H/2-margin
	}
}

// exit is how far the ray from (x,y) along unit (ux,uy) travels inside the pad.
func (p pePad) exit(x, y, ux, uy float64) float64 {
	t := math.Inf(1)
	if ux > 1e-9 {
		t = math.Min(t, (p.X+p.W/2-x)/ux)
	} else if ux < -1e-9 {
		t = math.Min(t, (p.X-p.W/2-x)/ux)
	}
	if uy > 1e-9 {
		t = math.Min(t, (p.Y+p.H/2-y)/uy)
	} else if uy < -1e-9 {
		t = math.Min(t, (p.Y-p.H/2-y)/uy)
	}
	return math.Max(t, 0)
}

func peFromSnapshot(b *boardSnapshot) ([]peTrack, []pePad, []peVia, error) {
	if b.Copper == nil || (b.Copper.Availability["routing"] != "available" && b.Copper.Availability["lines"] != "available") {
		return nil, nil, nil, fmt.Errorf("board dump has no available track list — re-run `pcb dump --include-copper`")
	}
	var tracks []peTrack
	for _, raw := range b.Copper.Lines {
		m, _ := raw.(map[string]any)
		layer := int(asFloat(m["layer"]))
		if layer != 1 && layer != 2 && (layer < 15 || layer > 44) {
			continue
		}
		locked, _ := m["locked"].(bool)
		tracks = append(tracks, peTrack{ID: asString(m["primitiveId"]), Net: asString(m["net"]), Layer: layer,
			X1: asFloat(m["startX"]), Y1: asFloat(m["startY"]), X2: asFloat(m["endX"]), Y2: asFloat(m["endY"]),
			W: asFloat(m["lineWidth"]), Locked: locked})
	}
	var pads []pePad
	for _, c := range b.Components {
		for _, p := range c.Pads {
			if p.W <= 0 || p.H <= 0 {
				continue
			}
			kind := "RECT"
			if sh, ok := p.Shape.([]any); ok && len(sh) > 0 {
				kind = strings.ToUpper(asString(sh[0]))
			}
			pads = append(pads, pePad{Ref: c.Designator + "." + p.Number, Net: p.Net, Layer: p.Layer, X: p.X, Y: p.Y, W: p.W, H: p.H, Kind: kind})
		}
	}
	var vias []peVia
	for _, raw := range b.Copper.Vias {
		m, _ := raw.(map[string]any)
		vias = append(vias, peVia{Net: asString(m["net"]), X: asFloat(m["x"]), Y: asFloat(m["y"]), D: asFloat(m["diameter"])})
	}
	return tracks, pads, vias, nil
}

// padAt returns the same-net pad on the track's layer containing (x,y).
func padAt(pads []pePad, net string, layer int, x, y float64) (pePad, bool) {
	for _, p := range pads {
		if p.Net == net && net != "" && p.onLayer(layer) && p.contains(x, y) {
			return p, true
		}
	}
	return pePad{}, false
}

// peMinKeep is the shortest original-width remainder worth keeping (mil).
const peMinKeep = 5.0

// planNeckdown narrows the end of every unlocked track that is wider than the
// narrow side of the pad it enters: the stretch inside the pad plus `extra`
// beyond its edge becomes `width`, the rest keeps the original width.
func planNeckdown(tracks []peTrack, pads []pePad, width, extra, tol float64) ([]peEdit, []peSkip) {
	var edits []peEdit
	skips := []peSkip{}
	for _, t := range tracks {
		L := math.Hypot(t.X2-t.X1, t.Y2-t.Y1)
		if L < 1e-6 || t.W <= width+1e-6 {
			continue
		}
		ux, uy := (t.X2-t.X1)/L, (t.Y2-t.Y1)/L
		cut := [2]float64{0, 0} // narrow length measured from start / from end
		var pad string
		for end := 0; end < 2; end++ {
			x, y, dx, dy := t.X1, t.Y1, ux, uy
			if end == 1 {
				x, y, dx, dy = t.X2, t.Y2, -ux, -uy
			}
			p, ok := padAt(pads, t.Net, t.Layer, x, y)
			if !ok || t.W <= math.Min(p.W, p.H)+tol {
				continue
			}
			if t.Locked {
				skips = append(skips, peSkip{Track: t.ID, Pad: p.Ref, Reason: "locked track wider than its pad — unlock deliberately first"})
				continue
			}
			cut[end] = p.exit(x, y, dx, dy) + extra
			pad = strings.TrimPrefix(pad+","+p.Ref, ",")
		}
		if cut[0] == 0 && cut[1] == 0 {
			continue
		}
		e := peEdit{DeleteID: t.ID, Pad: pad, Net: t.Net, From: t.W, To: width}
		pt := func(s float64) [2]float64 { return [2]float64{round2(t.X1 + ux*s), round2(t.Y1 + uy*s)} }
		a, b := cut[0], L-cut[1]
		mk := func(s0, s1, w float64) {
			if s1-s0 > 1e-6 {
				e.Creates = append(e.Creates, peLine{Net: t.Net, Layer: t.Layer, Width: w, From: pt(s0), To: pt(s1)})
			}
		}
		// A stretch of the original width shorter than peMinKeep is only a
		// sliver between two cuts or after one: the whole segment goes narrow.
		if b-a < peMinKeep {
			mk(0, L, width)
		} else {
			mk(0, a, width)
			mk(a, b, t.W)
			mk(b, L, width)
		}
		edits = append(edits, e)
	}
	return edits, skips
}

func peSegDist(px, py, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	l2 := dx*dx + dy*dy
	t := 0.0
	if l2 > 0 {
		t = math.Max(0, math.Min(1, ((px-ax)*dx+(py-ay)*dy)/l2))
	}
	return math.Hypot(px-(ax+t*dx), py-(ay+t*dy))
}

func peSegSeg(a, b, c, d [2]float64) float64 {
	if peSegsCross(a, b, c, d) {
		return 0
	}
	return math.Min(math.Min(peSegDist(a[0], a[1], c[0], c[1], d[0], d[1]), peSegDist(b[0], b[1], c[0], c[1], d[0], d[1])),
		math.Min(peSegDist(c[0], c[1], a[0], a[1], b[0], b[1]), peSegDist(d[0], d[1], a[0], a[1], b[0], b[1])))
}

func peSegsCross(a, b, c, d [2]float64) bool {
	o := func(p, q, r [2]float64) float64 { return (q[0]-p[0])*(r[1]-p[1]) - (q[1]-p[1])*(r[0]-p[0]) }
	return o(a, b, c)*o(a, b, d) < 0 && o(c, d, a)*o(c, d, b) < 0
}

func peSegRect(a, b [2]float64, p pePad) float64 {
	if p.contains(a[0], a[1]) || p.contains(b[0], b[1]) {
		return 0
	}
	x0, y0, x1, y1 := p.X-p.W/2, p.Y-p.H/2, p.X+p.W/2, p.Y+p.H/2
	edges := [][2][2]float64{{{x0, y0}, {x1, y0}}, {{x1, y0}, {x1, y1}}, {{x1, y1}, {x0, y1}}, {{x0, y1}, {x0, y0}}}
	d := math.Inf(1)
	for _, e := range edges {
		d = math.Min(d, peSegSeg(a, b, e[0], e[1]))
	}
	return d
}

// peLineClear checks a new line against every other-net copper object.
func peLineClear(l peLine, tracks []peTrack, pads []pePad, vias []peVia, clr float64) (bool, string) {
	for _, p := range pads {
		if p.Net != l.Net && p.onLayer(l.Layer) && peSegRect(l.From, l.To, p) < clr+l.Width/2 {
			return false, "pad " + p.Ref
		}
	}
	for _, t := range tracks {
		if t.Net != l.Net && t.Layer == l.Layer && peSegSeg(l.From, l.To, [2]float64{t.X1, t.Y1}, [2]float64{t.X2, t.Y2}) < clr+(l.Width+t.W)/2 {
			return false, "track " + t.ID + " (" + t.Net + ")"
		}
	}
	for _, v := range vias {
		if v.Net != l.Net && peSegDist(v.X, v.Y, l.From[0], l.From[1], l.To[0], l.To[1]) < clr+l.Width/2+v.D/2 {
			return false, "via (" + v.Net + ")"
		}
	}
	return true, ""
}

// planTeardrops adds two tapered lines per track→pad entry: from points on the
// pad edge either side of the track to a point on the track `length` beyond the
// pad edge (default: the pad's width across the track). Entries whose pad is not
// wider than the track, whose segment is too short, or whose lines would not
// keep `clr` from other-net copper are skipped with the reason; a re-run skips
// entries that already carry the same lines.
func planTeardrops(tracks []peTrack, pads []pePad, vias []peVia, clr float64, exclude map[string]bool) ([]peLine, []peSkip) {
	return planTeardropsScaled(tracks, pads, vias, clr, exclude, 1, 1)
}

func planTeardropsScaled(tracks []peTrack, pads []pePad, vias []peVia, clr float64, exclude map[string]bool, lengthScale, baseScale float64) ([]peLine, []peSkip) {
	var lines []peLine
	skips := []peSkip{}
	have := func(a, b [2]float64, net string, layer int) bool {
		for _, t := range tracks {
			if t.Net == net && t.Layer == layer &&
				((math.Hypot(t.X1-a[0], t.Y1-a[1]) < 0.5 && math.Hypot(t.X2-b[0], t.Y2-b[1]) < 0.5) ||
					(math.Hypot(t.X2-a[0], t.Y2-a[1]) < 0.5 && math.Hypot(t.X1-b[0], t.Y1-b[1]) < 0.5)) {
				return true
			}
		}
		return false
	}
	// onInterior: (x,y) lies inside another same-net track, away from its ends —
	// the far end of a teardrop line. Such a line is a teardrop, not an entry.
	onInterior := func(self string, net string, layer int, x, y float64) bool {
		for _, o := range tracks {
			if o.ID == self || o.Net != net || o.Layer != layer {
				continue
			}
			if peSegDist(x, y, o.X1, o.Y1, o.X2, o.Y2) < 0.5 && math.Hypot(x-o.X1, y-o.Y1) > 0.5 && math.Hypot(x-o.X2, y-o.Y2) > 0.5 {
				return true
			}
		}
		return false
	}
	// straight follows same-net collinear pieces from (x,y) along (ux,uy): the
	// host splits a track wherever another track ends on it, so one straight run
	// can be several primitives. It returns the run's far end and the joints.
	straight := func(self string, net string, layer int, x, y, ux, uy float64) (float64, float64, []float64) {
		var joints []float64
		dist := 0.0
		// ends counts track ends at (x,y): beyond 2 (the piece we came along and
		// one straight continuation) the point is a junction and the run stops.
		ends := func(x, y float64) int {
			n := 0
			for _, o := range tracks {
				if o.Net == net && o.Layer == layer && (math.Hypot(o.X1-x, o.Y1-y) < 0.05 || math.Hypot(o.X2-x, o.Y2-y) < 0.05) {
					n++
				}
			}
			return n
		}
		for guard := 0; guard < 32; guard++ {
			if ends(x, y) > 2 {
				break
			}
			found := false
			for _, o := range tracks {
				if o.ID == self || o.Net != net || o.Layer != layer {
					continue
				}
				var nx, ny float64
				switch {
				case math.Hypot(o.X1-x, o.Y1-y) < 0.05:
					nx, ny = o.X2, o.Y2
				case math.Hypot(o.X2-x, o.Y2-y) < 0.05:
					nx, ny = o.X1, o.Y1
				default:
					continue
				}
				l := math.Hypot(nx-x, ny-y)
				if l < 1e-6 || math.Abs((nx-x)/l*uy-(ny-y)/l*ux) > 0.01 || (nx-x)*ux+(ny-y)*uy <= 0 {
					continue
				}
				joints = append(joints, dist)
				dist += l
				x, y, self, found = nx, ny, o.ID, true
				break
			}
			if !found {
				break
			}
		}
		return x, y, joints
	}
	for _, t := range tracks {
		if exclude[t.Net] || t.Net == "" {
			continue
		}
		L0 := math.Hypot(t.X2-t.X1, t.Y2-t.Y1)
		if L0 < 1e-6 {
			continue
		}
		for end := 0; end < 2; end++ {
			x, y, ux, uy := t.X1, t.Y1, (t.X2-t.X1)/L0, (t.Y2-t.Y1)/L0
			fx, fy := t.X2, t.Y2
			if end == 1 {
				x, y, ux, uy = t.X2, t.Y2, -ux, -uy
				fx, fy = t.X1, t.Y1
			}
			if _, ok := padAt(pads, t.Net, t.Layer, x, y); !ok {
				continue
			}
			ex, ey, joints := straight(t.ID, t.Net, t.Layer, fx, fy, ux, uy)
			for i := range joints {
				joints[i] += L0
			}
			joints = append([]float64{L0}, joints...)
			fx, fy = ex, ey
			L := math.Hypot(fx-x, fy-y)
			joints = append(joints, L) // the run's far end (a junction or a bend)
			p, ok := padAt(pads, t.Net, t.Layer, x, y)
			if !ok || onInterior(t.ID, t.Net, t.Layer, fx, fy) {
				continue
			}
			// Only unambiguous entries: along an axis, through a pad side, and the
			// pad's only track. Angled or shared entries overlapped existing copper
			// and crossed pad corners live (track-over-pad, duplicate segments).
			if math.Abs(ux) > 1e-3 && math.Abs(uy) > 1e-3 {
				skips = append(skips, peSkip{Track: t.ID, Pad: p.Ref, Reason: "entry not along an axis"})
				continue
			}
			if n := padEntries(tracks, p, t.Layer); n > 1 {
				skips = append(skips, peSkip{Track: t.ID, Pad: p.Ref, Reason: fmt.Sprintf("pad has %d track entries", n)})
				continue
			}
			vx, vy := -uy, ux
			halfPerp := math.Abs(vx)*p.W/2 + math.Abs(vy)*p.H/2
			h := halfPerp - t.W/2 - 0.5
			if h < t.W/2 {
				skips = append(skips, peSkip{Track: t.ID, Pad: p.Ref, Reason: "pad not wider than the track"})
				continue
			}
			te := p.exit(x, y, ux, uy)
			// A short pad-to-pad link (adjacent same-net pins bridged): the exposed
			// copper between the pads is the whole joint, a teardrop has no room.
			free := L - te // exposed track this teardrop may use
			if q, ok := padAt(pads, t.Net, t.Layer, fx, fy); ok {
				free = L - te - q.exit(fx, fy, -ux, -uy)
				if free < 2*t.W {
					skips = append(skips, peSkip{Track: t.ID, Pad: p.Ref, Reason: "short pad-to-pad link"})
					continue
				}
				// both ends get a teardrop: each takes at most half, so they never cross
				free /= 2
			}
			// The tip stays 1 mil inside the segment so it lands on the track's
			// interior (an endpoint would read as a corner); short exits still get
			// a short teardrop down to 0.75 track widths.
			length := math.Min(free-1, math.Max(2*halfPerp, 3*t.W)*lengthScale)
			if length < 0.75*t.W {
				skips = append(skips, peSkip{Track: t.ID, Pad: p.Ref, Reason: "track leaves the pad too short for a teardrop"})
				continue
			}
			// Keep a teardrop's proportion (half-angle ≤ ~35°): a base wider than
			// the run is a fan, and its lines meet the track at an acute corner.
			h = math.Min(h*baseScale, 0.7*length)
			if h < 0.05 {
				skips = append(skips, peSkip{Track: t.ID, Pad: p.Ref, Reason: "teardrop base too small after scaling"})
				continue
			}
			bx, by := x+ux*te, y+uy*te
			// Keep both tips off a joint between collinear pieces (a tip on a
			// joint is a track endpoint, i.e. a corner).
			for _, j := range joints {
				if d := te + length - j; d > -2.5 && d < 1.5 {
					length = j - te - 2.5
				}
			}
			if length < 0.75*t.W {
				skips = append(skips, peSkip{Track: t.ID, Pad: p.Ref, Reason: "track leaves the pad too short for a teardrop"})
				continue
			}
			h = math.Min(h, 0.7*length)
			// The two tips sit 1 mil apart on the track: a shared endpoint would
			// make the pair itself a sharp corner.
			tips := [2][2]float64{{round2(bx + ux*length), round2(by + uy*length)}, {round2(bx + ux*(length-1)), round2(by + uy*(length-1))}}
			var pair []peLine
			reason := ""
			for _, scale := range []float64{1, 0.8, 0.6, 0.45, 0.3} {
				pair, reason = nil, ""
				for i, s := range []float64{1, -1} {
					fx0, fy0 := bx-ux*t.W/2+s*vx*h*scale, by-uy*t.W/2+s*vy*h*scale
					if !p.insideCopper(fx0, fy0, 0.5) {
						reason = "pad outline too small for the teardrop base"
						break
					}
					from := [2]float64{round2(fx0), round2(fy0)}
					l := peLine{Net: t.Net, Layer: t.Layer, Width: t.W, From: from, To: tips[i], Reason: "teardrop " + p.Ref}
					if ok, why := peLineClear(l, tracks, pads, vias, clr); !ok {
						reason = "clearance to " + why
						break
					}
					pair = append(pair, l)
				}
				if reason == "" {
					break
				}
			}
			if reason != "" {
				skips = append(skips, peSkip{Track: t.ID, Pad: p.Ref, Reason: reason})
				continue
			}
			if have(pair[0].From, pair[0].To, t.Net, t.Layer) {
				continue
			}
			lines = append(lines, pair...)
		}
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].Reason < lines[j].Reason })
	return lines, skips
}

// padEntries counts the same-layer tracks of the pad's net with an end on it.
func padEntries(tracks []peTrack, p pePad, layer int) int {
	n := 0
	for _, t := range tracks {
		if t.Net == p.Net && t.Layer == layer && (p.contains(t.X1, t.Y1) || p.contains(t.X2, t.Y2)) {
			n++
		}
	}
	return n
}

// teardropRemovals requires every planned line to be covered exactly once, end
// to end, by same-net, same-layer, same-width pieces of a fresh readback. The
// tolerance is a fixed mil value (it must not grow with line length); a locked,
// missing or overlapping piece rejects the whole plan.
func teardropRemovals(tracks []peTrack, planned []peLine) ([]string, error) {
	const eps = 0.05
	seen := map[string]bool{}
	var ids []string
	for i, l := range planned {
		dx, dy := l.To[0]-l.From[0], l.To[1]-l.From[1]
		length := math.Hypot(dx, dy)
		if length <= eps || !peFinite(length) || l.Width <= 0 || !peFinite(l.Width) {
			return nil, fmt.Errorf("invalid planned line %d", i)
		}
		type piece struct {
			start, end float64
			id         string
		}
		var pieces []piece
		project := func(x, y float64) (float64, bool) {
			along := ((x-l.From[0])*dx + (y-l.From[1])*dy) / length
			off := math.Abs((x-l.From[0])*dy-(y-l.From[1])*dx) / length
			return along, peFinite(along) && along >= -eps && along <= length+eps && off <= eps
		}
		for _, t := range tracks {
			if t.Net != l.Net || t.Layer != l.Layer {
				continue
			}
			a, aok := project(t.X1, t.Y1)
			b, bok := project(t.X2, t.Y2)
			if !aok || !bok {
				continue
			}
			if t.ID == "" || t.Locked || !peFinite(t.W) || math.Abs(t.W-l.Width) > 0.01 {
				return nil, fmt.Errorf("planned line %d: matching track %q is locked, invalid or has changed width", i, t.ID)
			}
			if a > b {
				a, b = b, a
			}
			if b-a <= 1e-6 {
				return nil, fmt.Errorf("planned line %d: degenerate track %s", i, t.ID)
			}
			pieces = append(pieces, piece{a, b, t.ID})
		}
		sort.Slice(pieces, func(i, j int) bool { return pieces[i].start < pieces[j].start })
		end := 0.0
		for _, p := range pieces {
			if math.Abs(p.start-end) > eps || seen[p.id] {
				return nil, fmt.Errorf("planned line %d: gap, overlap or ambiguous track %s", i, p.id)
			}
			seen[p.id] = true
			ids = append(ids, p.id)
			end = p.end
		}
		if len(pieces) == 0 || math.Abs(end-length) > eps {
			return nil, fmt.Errorf("planned line %d: missing or changed geometry; capture again and reconcile the old plan", i)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// teardropClearance is the board's rule clearance + 0.5 mil, or fallback when the
// dump carries no rules; planning and modification must use the same value.
func teardropClearance(b *boardSnapshot, fallback float64) float64 {
	if b.Rules == nil {
		return fallback
	}
	if c := math.Max(b.Rules.ClearanceTrackTrackMil, b.Rules.ClearanceMil); c > 0 {
		return c + 0.5
	}
	return fallback
}

func peFinite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func teardropKey(l peLine) string { return fmt.Sprintf("%s/%d/%s", l.Net, l.Layer, l.Reason) }

func readTeardropPlan(path string) ([]peLine, error) {
	var plan struct {
		Units string   `json:"units"`
		Lines []peLine `json:"lines"`
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(raw, &plan); err != nil {
		return nil, err
	}
	if plan.Units != "mil" || len(plan.Lines) == 0 {
		return nil, fmt.Errorf("expected a non-empty teardrop plan with units=mil")
	}
	pairs := map[string][]peLine{}
	for i, l := range plan.Lines {
		if l.Net == "" || !strings.HasPrefix(l.Reason, "teardrop ") || strings.TrimSpace(strings.TrimPrefix(l.Reason, "teardrop ")) == "" || (l.Layer != 1 && l.Layer != 2 && (l.Layer < 15 || l.Layer > 44)) {
			return nil, fmt.Errorf("invalid teardrop identity at line %d", i)
		}
		for _, v := range []float64{l.From[0], l.From[1], l.To[0], l.To[1], l.Width} {
			if !peFinite(v) {
				return nil, fmt.Errorf("non-finite line %d", i)
			}
		}
		if l.Width <= 0 || math.Hypot(l.To[0]-l.From[0], l.To[1]-l.From[1]) <= 0.05 {
			return nil, fmt.Errorf("invalid line geometry %d", i)
		}
		key := teardropKey(l)
		pairs[key] = append(pairs[key], l)
	}
	for key, pair := range pairs {
		if len(pair) != 2 || pair[0].Width != pair[1].Width || pair[0].From == pair[1].From || pair[0].To == pair[1].To {
			return nil, fmt.Errorf("expected two distinct same-width lines for %s", key)
		}
	}
	return plan.Lines, nil
}

func teardropDeleteSteps(ids []string) []map[string]any {
	var steps []map[string]any
	for i := 0; i < len(ids); i += 40 {
		steps = append(steps, map[string]any{"id": fmt.Sprintf("del-%d", i/40), "action": "pcb.route.delete", "payload": map[string]any{"primitiveIds": ids[i:min(i+40, len(ids))], "kind": "track"}})
	}
	return steps
}

func modifyTeardrops(tracks []peTrack, pads []pePad, vias []peVia, previous []peLine, clr, lengthScale, baseScale float64) ([]string, []peLine, error) {
	for _, v := range []float64{clr, lengthScale, baseScale} {
		if !peFinite(v) || v <= 0 {
			return nil, nil, fmt.Errorf("clearance and scales must be finite and positive")
		}
	}
	if baseScale > 1 {
		return nil, nil, fmt.Errorf("base-scale must be <= 1 to keep the base inside the pad")
	}
	ids, err := teardropRemovals(tracks, previous)
	if err != nil {
		return nil, nil, err
	}
	removed := map[string]bool{}
	for _, id := range ids {
		removed[id] = true
	}
	remaining := []peTrack{}
	for _, t := range tracks {
		if !removed[t.ID] {
			remaining = append(remaining, t)
		}
	}
	wanted := map[string]int{}
	widths := map[string]float64{}
	for _, l := range previous {
		wanted[teardropKey(l)]++
		widths[teardropKey(l)] = l.Width
	}
	candidates, _ := planTeardropsScaled(remaining, pads, vias, clr, nil, lengthScale, baseScale)
	var lines []peLine
	found := map[string]int{}
	for _, l := range candidates {
		key := teardropKey(l)
		if wanted[key] > 0 {
			if math.Abs(l.Width-widths[key]) > 0.01 {
				return nil, nil, fmt.Errorf("cannot rebuild %s: parent track width changed; reconcile the old plan first", key)
			}
			lines = append(lines, l)
			found[key]++
		}
	}
	for key, n := range wanted {
		if found[key] != n {
			return nil, nil, fmt.Errorf("cannot rebuild %s safely (%d/%d lines); no deletion planned", key, found[key], n)
		}
	}
	return ids, lines, nil
}

// teardropOutputPaths refuses to overwrite an input or any existing file, so the
// old plan stays traceable and a stale playbook is never mistaken for a new one.
func teardropOutputPaths(out string, inputs ...string) (string, error) {
	playbook := strings.TrimSuffix(out, ".json") + ".playbook.json"
	for _, name := range []string{out, playbook} {
		abs, err := filepath.Abs(name)
		if err != nil {
			return "", err
		}
		for _, in := range inputs {
			p, err := filepath.Abs(in)
			if err != nil {
				return "", err
			}
			if abs == p {
				return "", fmt.Errorf("output must not overwrite input %s", in)
			}
		}
		if _, err := os.Lstat(name); err == nil {
			return "", fmt.Errorf("output already exists: %s; choose a new --out", name)
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	return playbook, nil
}

func newPcbTeardropEditCmd(stdout io.Writer, modify bool) *cobra.Command {
	var board, from, out, project, doc string
	var clr, lengthScale, baseScale float64
	name := "teardrop-delete-plan"
	if modify {
		name = "teardrop-modify-plan"
	}
	short := "规划线条形泪滴的删除（离线，输出 typed apply 剧本）"
	if modify {
		short = "规划线条形泪滴的尺寸修改（离线，输出 typed apply 剧本）"
	}
	cmd := &cobra.Command{Use: name, Short: short, Args: cobra.NoArgs,
		Long: `读取新 pcb dump --include-copper 和旧 teardrop-plan，仅处理旧计划的线条形泪滴。
同网、同层、同宽的完整线或连续碎段必须唯一覆盖旧几何；锁定、缺段或漂移时拒绝。
修改按当前焊盘/主走线的默认几何乘比例重算，只重建旧计划的入口，保持线宽。
length-scale > 0，base-scale 在 (0,1]；长度仍受走线空间限制，底边仍检查轮廓和间距。
输出计划和 .playbook.json，不写 EDA。保持板未变再 easyeda apply，随后重铺铜、保存、重载、回读与 DRC。
多步剧本不是事务；中途失败先回读，禁止盲目重放。原生 TEARDROP 对象不在本命令范围。`,
		Example: "  easyeda pcb " + name + " --board fresh.json --from tear.json --out next.json --apply-project ceshi --apply-doc PCB1",
		RunE: func(cmd *cobra.Command, args []string) error {
			playbook, err := teardropOutputPaths(out, board, from)
			if err != nil {
				return err
			}
			prev, err := readTeardropPlan(from)
			if err != nil {
				return err
			}
			b, err := loadBoardSnapshotPath(board)
			if err != nil {
				return err
			}
			tracks, pads, vias, err := peFromSnapshot(b)
			if err != nil {
				return err
			}
			var ids []string
			var lines []peLine
			if modify {
				if b.Copper.Availability["vias"] != "available" || b.ComponentsAvailability != "available" {
					return fmt.Errorf("modification requires available vias and components in a fresh dump")
				}
				if !cmd.Flags().Changed("clearance") {
					clr = teardropClearance(b, clr)
				}
				ids, lines, err = modifyTeardrops(tracks, pads, vias, prev, clr, lengthScale, baseScale)
			} else {
				ids, err = teardropRemovals(tracks, prev)
			}
			if err != nil {
				return err
			}
			steps := teardropDeleteSteps(ids)
			for i, l := range lines {
				steps = append(steps, peLineStep(fmt.Sprintf("tear-%d", i), l))
			}
			plan := map[string]any{"units": "mil", "from": from, "primitiveIds": ids, "lines": lines}
			if modify {
				plan["clearance"] = clr
				plan["lengthScale"] = lengthScale
				plan["baseScale"] = baseScale
			}
			if err = peWrite(out, plan); err != nil {
				return err
			}
			if err = peWrite(playbook, pePlaybook(name, project, doc, steps)); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "%s: delete %d primitive(s), create %d line(s) → %s (+ .playbook.json)\n", name, len(ids), len(lines), out)
			return nil
		}}
	cmd.Flags().StringVar(&board, "board", "", "新 pcb dump --include-copper JSON")
	cmd.Flags().StringVar(&from, "from", "", "原 teardrop-plan 或上次修改计划 JSON")
	cmd.Flags().StringVar(&out, "out", "", "新计划路径（不得已存在）")
	cmd.Flags().StringVar(&project, "apply-project", "", "剧本目标工程")
	cmd.Flags().StringVar(&doc, "apply-doc", "", "剧本目标 PCB")
	for _, f := range []string{"board", "from", "out"} {
		_ = cmd.MarkFlagRequired(f)
	}
	if modify {
		cmd.Flags().Float64Var(&clr, "clearance", 6.5, "与异网焊盘、走线和过孔的间距，mil；默认使用回读规则 +0.5")
		cmd.Flags().Float64Var(&lengthScale, "length-scale", 1, "相对默认长度的正比例，受可用空间限制")
		cmd.Flags().Float64Var(&baseScale, "base-scale", 1, "相对默认底边半宽的比例 (0,1]")
		cmd.Example += " --length-scale 0.8 --base-scale 0.8"
	}
	return cmd
}

func peLineStep(id string, l peLine) map[string]any {
	return map[string]any{"id": id, "action": "pcb.line.create", "payload": map[string]any{
		"startX": l.From[0], "startY": l.From[1], "endX": l.To[0], "endY": l.To[1],
		"net": l.Net, "layer": l.Layer, "lineWidth": l.Width}}
}

func pePlaybook(name, project, doc string, steps []map[string]any) map[string]any {
	steps = append(steps, map[string]any{"id": "save", "action": "pcb.save", "checkpoint": true})
	meta := map[string]any{"name": name}
	if project != "" {
		meta["project"] = project
	}
	if doc != "" {
		meta["doc"] = doc
	}
	return map[string]any{"version": 1, "requireFullExecution": true, "meta": meta, "defaults": map[string]any{"timeoutSec": 90}, "vars": map[string]any{}, "steps": steps}
}

func peWrite(path string, v any) error {
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func newPcbPadEntryCmds(stdout io.Writer) []*cobra.Command {
	load := func(path string) ([]peTrack, []pePad, []peVia, *boardSnapshot, error) {
		b, err := loadBoardSnapshotPath(path)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		t, p, v, err := peFromSnapshot(b)
		return t, p, v, b, err
	}
	var board, out, project, doc string
	var width, extra, tol float64
	neck := &cobra.Command{
		Use:   "neckdown-plan",
		Short: "Plan narrowing track ends wider than the pad they enter (offline; emits an apply playbook)",
		Long: `Read a fresh 'pcb dump --include-copper' and, for every unlocked track whose
width exceeds the narrow side of the same-net pad it ends in, plan to replace it by
a --width stretch from the pad to --extra mil beyond the pad edge plus the original
width for the rest. Fine-pitch pads (0.4 mm QFN, 7.9 mil) make an exam rule such as
"track not wider than its pad" fail for 10/20 mil power traces; this fixes only the
pad entry. Locked tracks are reported, never changed. Writes <out> (plan) and
<out>.playbook.json; run 'easyeda apply' on it, then pour-rebuild, save, reload and
DRC. The new width must satisfy the net's track rule minimum.`,
		Args:    cobra.NoArgs,
		Example: `  easyeda pcb neckdown-plan --board board.json --width 8 --out neck.json --apply-project ceshi --apply-doc PCB1`,
		RunE: func(cmd *cobra.Command, args []string) error {
			tracks, pads, _, _, err := load(board)
			if err != nil {
				return err
			}
			edits, skips := planNeckdown(tracks, pads, width, extra, tol)
			var steps []map[string]any
			for i, e := range edits {
				steps = append(steps, map[string]any{"id": fmt.Sprintf("del-%d", i), "action": "pcb.route.delete",
					"payload": map[string]any{"primitiveIds": []string{e.DeleteID}, "kind": "track"}})
				for j, l := range e.Creates {
					steps = append(steps, peLineStep(fmt.Sprintf("new-%d-%d", i, j), l))
				}
			}
			plan := map[string]any{"units": "mil", "width": width, "extra": extra, "edits": edits, "skipped": skips}
			if err := peWrite(out, plan); err != nil {
				return err
			}
			if err := peWrite(strings.TrimSuffix(out, ".json")+".playbook.json", pePlaybook("neckdown", project, doc, steps)); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "neckdown-plan: %d track(s) to narrow, %d skipped → %s (+ .playbook.json)\n", len(edits), len(skips), out)
			return nil
		},
	}
	for _, c := range []*cobra.Command{neck} {
		c.Flags().StringVar(&board, "board", "", "fresh `pcb dump --include-copper` JSON (required)")
		c.Flags().StringVar(&out, "out", "", "plan JSON path; the playbook goes next to it (required)")
		c.Flags().StringVar(&project, "apply-project", "", "bind the playbook to this project")
		c.Flags().StringVar(&doc, "apply-doc", "", "bind the playbook to this PCB document")
		_ = c.MarkFlagRequired("board")
		_ = c.MarkFlagRequired("out")
	}
	neck.Flags().Float64Var(&width, "width", 8, "width of the pad-entry stretch (mil)")
	neck.Flags().Float64Var(&extra, "extra", 10, "how far beyond the pad edge the narrow stretch runs (mil)")
	neck.Flags().Float64Var(&tol, "tolerance", 0.5, "a track this much wider than the pad still counts as fitting (mil)")

	var tBoard, tOut, tProject, tDoc, removeFrom string
	var clr float64
	var excludeNets []string
	tear := &cobra.Command{
		Use:   "teardrop-plan",
		Short: "Plan line-shaped teardrops at track→pad entries (offline; emits an apply playbook)",
		Long: `EasyEDA's native teardrop object has no public create API, so this plans the
line-shaped teardrop an exam accepts ("添加圆弧或线条形状泪滴"): for each track that ends
in a same-net pad wider than the track, two tracks of the track's width run from the
pad edge either side of it to a point on the track (default length: the pad width
across the track). Each line must keep --clearance from other-net pads, tracks and
vias, else the entry is skipped with the reason. Re-running skips entries that
already carry the lines. Exclude nets whose geometry is tuned (RF feed) with
--exclude-net. Writes <out> and <out>.playbook.json; apply, then pour-rebuild, save,
reload and DRC. Only unambiguous entries get one: along an axis, through a pad
side, and the pad's only track. --remove-from <earlier plan> instead emits a
playbook deleting that plan's lines (and the pieces the host split them into).`,
		Args: cobra.NoArgs,
		Example: `  easyeda pcb teardrop-plan --board board.json --exclude-net RF_ANT --out tear.json --apply-project ceshi --apply-doc PCB1
  easyeda pcb teardrop-plan --board board.json --remove-from tear.json --out untear.json --apply-project ceshi --apply-doc PCB1`,
		RunE: func(cmd *cobra.Command, args []string) error {
			tracks, pads, vias, b, err := load(tBoard)
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("clearance") {
				clr = teardropClearance(b, clr)
			}
			ex := map[string]bool{}
			for _, n := range excludeNets {
				ex[n] = true
			}
			if removeFrom != "" {
				if _, err := teardropOutputPaths(tOut, tBoard, removeFrom); err != nil {
					return err
				}
				prev, err := readTeardropPlan(removeFrom)
				if err != nil {
					return err
				}
				ids, err := teardropRemovals(tracks, prev)
				if err != nil {
					return err
				}
				steps := teardropDeleteSteps(ids)
				if err := peWrite(tOut, map[string]any{"removeFrom": removeFrom, "planned": len(prev), "primitiveIds": ids}); err != nil {
					return err
				}
				if err := peWrite(strings.TrimSuffix(tOut, ".json")+".playbook.json", pePlaybook("teardrop-remove", tProject, tDoc, steps)); err != nil {
					return err
				}
				fmt.Fprintf(stdout, "teardrop-plan --remove-from: %d primitive(s) for %d planned line(s) → %s (+ .playbook.json)\n", len(ids), len(prev), tOut)
				return nil
			}
			lines, skips := planTeardrops(tracks, pads, vias, clr, ex)
			var steps []map[string]any
			for i, l := range lines {
				steps = append(steps, peLineStep(fmt.Sprintf("tear-%d", i), l))
			}
			plan := map[string]any{"units": "mil", "clearance": clr, "lines": lines, "skipped": skips}
			if err := peWrite(tOut, plan); err != nil {
				return err
			}
			if err := peWrite(strings.TrimSuffix(tOut, ".json")+".playbook.json", pePlaybook("teardrop", tProject, tDoc, steps)); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "teardrop-plan: %d line(s) for %d entr(ies), %d skipped → %s (+ .playbook.json)\n", len(lines), len(lines)/2, len(skips), tOut)
			return nil
		},
	}
	tear.Flags().StringVar(&tBoard, "board", "", "fresh `pcb dump --include-copper` JSON (required)")
	tear.Flags().StringVar(&tOut, "out", "", "plan JSON path; the playbook goes next to it (required)")
	tear.Flags().StringVar(&tProject, "apply-project", "", "bind the playbook to this project")
	tear.Flags().StringVar(&tDoc, "apply-doc", "", "bind the playbook to this PCB document")
	tear.Flags().Float64Var(&clr, "clearance", 6.5, "required gap to other-net copper (mil; default = dump rules + 0.5)")
	tear.Flags().StringSliceVar(&excludeNets, "exclude-net", nil, "nets to leave without teardrops (repeat or comma-separate)")
	tear.Flags().StringVar(&removeFrom, "remove-from", "", "instead of planning, emit a playbook deleting the teardrops of this earlier plan (exact lines + host-split pieces)")
	_ = tear.MarkFlagRequired("board")
	_ = tear.MarkFlagRequired("out")
	return []*cobra.Command{neck, tear, newPcbTeardropEditCmd(stdout, false), newPcbTeardropEditCmd(stdout, true)}
}
