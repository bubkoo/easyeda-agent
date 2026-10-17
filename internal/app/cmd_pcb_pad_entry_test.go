package app

import (
	"math"
	"testing"
)

func TestPlanNeckdownNarrowsOnlyThePadEntry(t *testing.T) {
	pads := []pePad{{Ref: "U1.29", Net: "VDD_SPI", Layer: 1, X: 134.2, Y: -102.4, W: 26.2, H: 7.9}}
	tracks := []peTrack{
		{ID: "t1", Net: "VDD_SPI", Layer: 1, X1: 134.2, Y1: -102.4, X2: 200, Y2: -102.4, W: 10},
		{ID: "locked", Net: "VDD_SPI", Layer: 1, X1: 134.2, Y1: -102.4, X2: 134.2, Y2: -150, W: 10, Locked: true},
		{ID: "thin", Net: "VDD_SPI", Layer: 1, X1: 134.2, Y1: -102.4, X2: 120, Y2: -90, W: 7},
	}
	edits, skips := planNeckdown(tracks, pads, 8, 10, 0.5)
	if len(edits) != 1 || edits[0].DeleteID != "t1" || len(edits[0].Creates) != 2 {
		t.Fatalf("edits=%+v", edits)
	}
	n, w := edits[0].Creates[0], edits[0].Creates[1]
	// pad half-length 13.1 from the centre + 10 beyond the edge = 23.1 mil narrow
	if n.Width != 8 || math.Abs(n.To[0]-(134.2+23.1)) > 0.01 || w.Width != 10 || w.To[0] != 200 {
		t.Fatalf("narrow=%+v rest=%+v", n, w)
	}
	if len(skips) != 1 || skips[0].Track != "locked" {
		t.Fatalf("skips=%+v", skips)
	}
}

func TestPlanTeardropsTwoLinesAndClearance(t *testing.T) {
	pad := pePad{Ref: "C1.1", Net: "N1", Layer: 1, X: 0, Y: 0, W: 19.7, H: 21.3}
	track := peTrack{ID: "t", Net: "N1", Layer: 1, X1: 0, Y1: 0, X2: 100, Y2: 0, W: 8}
	lines, skips := planTeardrops([]peTrack{track}, []pePad{pad}, nil, 6.5, nil)
	if len(lines) != 2 || len(skips) != 0 {
		t.Fatalf("lines=%+v skips=%+v", lines, skips)
	}
	if lines[0].To == lines[1].To || lines[0].To[1] != 0 || lines[1].To[1] != 0 || lines[0].To[0] <= 9.85 || lines[0].From[1] != -lines[1].From[1] {
		t.Fatalf("not a symmetric teardrop onto the track: %+v", lines)
	}
	// a foreign track hugging the entry blocks both scales → skipped with a reason
	foreign := peTrack{ID: "f", Net: "N2", Layer: 1, X1: 10, Y1: 12, X2: 60, Y2: 12, W: 8}
	lines, skips = planTeardrops([]peTrack{track, foreign}, []pePad{pad}, nil, 6.5, nil)
	if len(lines) != 0 || len(skips) != 1 {
		t.Fatalf("expected skip, lines=%+v skips=%+v", lines, skips)
	}
	// idempotent: existing teardrop lines are not planned again
	first, _ := planTeardrops([]peTrack{track}, []pePad{pad}, nil, 6.5, nil)
	with := []peTrack{track}
	for i, l := range first {
		with = append(with, peTrack{ID: string(rune('a' + i)), Net: l.Net, Layer: l.Layer, X1: l.From[0], Y1: l.From[1], X2: l.To[0], Y2: l.To[1], W: l.Width})
	}
	if again, _ := planTeardrops(with, []pePad{pad}, nil, 6.5, nil); len(again) != 0 {
		t.Fatalf("re-run planned %d lines", len(again))
	}
}

func TestPeFromSnapshotReadsRoutingAvailability(t *testing.T) {
	b := &boardSnapshot{Copper: &boardCopperSnapshot{Availability: map[string]string{"routing": "available"},
		Lines: []any{map[string]any{"primitiveId": "t", "net": "N", "layer": float64(1), "startX": float64(0), "startY": float64(0), "endX": float64(10), "endY": float64(0), "lineWidth": float64(8)}}}}
	tracks, _, _, err := peFromSnapshot(b)
	if err != nil || len(tracks) != 1 {
		t.Fatalf("tracks=%v err=%v", tracks, err)
	}
	if _, _, _, err := peFromSnapshot(&boardSnapshot{Copper: &boardCopperSnapshot{Availability: map[string]string{"routing": "unavailable"}}}); err == nil {
		t.Fatal("unavailable routing accepted")
	}
}

func TestPlanNeckdownToleranceAndSliver(t *testing.T) {
	pads := []pePad{{Ref: "C11.1", Net: "RF", Layer: 1, X: 0, Y: 0, W: 21.3, H: 19.7}, {Ref: "U1.3", Net: "P", Layer: 1, X: 100, Y: 0, W: 26.2, H: 7.9}}
	tracks := []peTrack{
		{ID: "rf", Net: "RF", Layer: 1, X1: 0, Y1: 0, X2: 60, Y2: 0, W: 20},     // 0.3 over the pad: fits
		{ID: "short", Net: "P", Layer: 1, X1: 100, Y1: 0, X2: 76, Y2: 0, W: 10}, // 24 long, 23.1 narrow → no sliver
	}
	edits, _ := planNeckdown(tracks, pads, 8, 10, 0.5)
	if len(edits) != 1 || edits[0].DeleteID != "short" || len(edits[0].Creates) != 1 || edits[0].Creates[0].Width != 8 {
		t.Fatalf("edits=%+v", edits)
	}
}

func TestPlanTeardropsSkipsShortPadBridge(t *testing.T) {
	pads := []pePad{{Ref: "U1.55", Net: "P", Layer: 1, X: -86.6, Y: 134.2, W: 7.9, H: 26.2}, {Ref: "U1.56", Net: "P", Layer: 1, X: -102.3, Y: 134.2, W: 7.9, H: 26.2}}
	bridge := peTrack{ID: "b", Net: "P", Layer: 1, X1: -86.6, Y1: 125, X2: -102.3, Y2: 125, W: 8}
	if lines, _ := planTeardrops([]peTrack{bridge}, pads, nil, 6.5, nil); len(lines) != 0 {
		t.Fatalf("teardrops on a pin-to-pin bridge: %+v", lines)
	}
}

func TestPlanTeardropsRoundPadBaseInsideCopper(t *testing.T) {
	pad := pePad{Ref: "J1.3", Net: "TX", Layer: 12, X: 280, Y: 480, W: 66.9, H: 66.9, Kind: "ELLIPSE"}
	track := peTrack{ID: "t", Net: "TX", Layer: 2, X1: 280, Y1: 480, X2: 280, Y2: 379, W: 8}
	lines, skips := planTeardrops([]peTrack{track}, []pePad{pad}, nil, 6.5, nil)
	if len(lines) != 2 {
		t.Fatalf("lines=%+v skips=%+v", lines, skips)
	}
	for _, l := range lines {
		if !pad.insideCopper(l.From[0], l.From[1], 0.4) {
			t.Fatalf("teardrop base outside the round pad: %+v", l)
		}
	}
}

func TestPlanTeardropsPadToPadHalves(t *testing.T) {
	pads := []pePad{{Ref: "R1.1", Net: "EN", Layer: 1, X: -283, Y: 40, W: 22.3, H: 21.3}, {Ref: "C6.1", Net: "EN", Layer: 1, X: -283.5, Y: -10, W: 19.7, H: 21.3}}
	link := peTrack{ID: "l", Net: "EN", Layer: 1, X1: -283, Y1: 40, X2: -283, Y2: -10, W: 8}
	lines, _ := planTeardrops([]peTrack{link}, pads, nil, 6.5, nil)
	if len(lines) != 4 {
		t.Fatalf("lines=%+v", lines)
	}
	for _, a := range lines[:2] {
		for _, b := range lines[2:] {
			if peSegsCross(a.From, a.To, b.From, b.To) {
				t.Fatalf("teardrops from both ends cross: %+v %+v", a, b)
			}
		}
	}
}

func TestPlanTeardropsKeepProportionOnShortStub(t *testing.T) {
	pad := pePad{Ref: "SW2.2", Net: "GND", Layer: 1, X: -244, Y: -380, W: 48.4, H: 73.2}
	stub := peTrack{ID: "s", Net: "GND", Layer: 1, X1: -244, Y1: -380, X2: -202, Y2: -380, W: 10}
	lines, _ := planTeardrops([]peTrack{stub}, []pePad{pad}, nil, 6.5, nil)
	if len(lines) != 2 {
		t.Fatalf("lines=%+v", lines)
	}
	for _, l := range lines {
		dx, dy := l.To[0]-l.From[0], l.To[1]-l.From[1]
		if ang := math.Atan2(math.Abs(dy), math.Abs(dx)) * 180 / math.Pi; ang > 40 {
			t.Fatalf("teardrop line %.1f° off the track: %+v", ang, l)
		}
	}
}

func TestPlanTeardropsAcrossSplitPieces(t *testing.T) {
	pad := pePad{Ref: "C1.1", Net: "N1", Layer: 1, X: 0, Y: 0, W: 19.7, H: 21.3}
	a := peTrack{ID: "a", Net: "N1", Layer: 1, X1: 0, Y1: 0, X2: 20, Y2: 0, W: 8}
	b := peTrack{ID: "b", Net: "N1", Layer: 1, X1: 20, Y1: 0, X2: 100, Y2: 0, W: 8}
	lines, skips := planTeardrops([]peTrack{a, b}, []pePad{pad}, nil, 6.5, nil)
	if len(lines) != 2 {
		t.Fatalf("lines=%+v skips=%+v", lines, skips)
	}
	for _, l := range lines {
		if math.Abs(l.To[0]-20) < 1.5 {
			t.Fatalf("tip on the joint between pieces: %+v", l)
		}
		if l.To[0] < 9.85+0.75*8 {
			t.Fatalf("teardrop too short across pieces: %+v", l)
		}
	}
}

func TestPlanTeardropsOnlyUnambiguousEntries(t *testing.T) {
	pad := pePad{Ref: "C1.1", Net: "N1", Layer: 1, X: 0, Y: 0, W: 19.7, H: 21.3}
	angled := peTrack{ID: "x", Net: "N1", Layer: 1, X1: 0, Y1: 0, X2: 60, Y2: 30, W: 8}
	if lines, _ := planTeardrops([]peTrack{angled}, []pePad{pad}, nil, 6.5, nil); len(lines) != 0 {
		t.Fatalf("angled entry got a teardrop: %+v", lines)
	}
	a := peTrack{ID: "a", Net: "N1", Layer: 1, X1: 0, Y1: 0, X2: 60, Y2: 0, W: 8}
	b := peTrack{ID: "b", Net: "N1", Layer: 1, X1: 0, Y1: 0, X2: 0, Y2: 60, W: 8}
	if lines, _ := planTeardrops([]peTrack{a, b}, []pePad{pad}, nil, 6.5, nil); len(lines) != 0 {
		t.Fatalf("shared pad got teardrops: %+v", lines)
	}
}

func TestTeardropRemovalsIncludeSplitPieces(t *testing.T) {
	planned := []peLine{{Net: "N", Layer: 1, Width: 8, From: [2]float64{0, 6}, To: [2]float64{30, 0}}}
	tracks := []peTrack{
		{ID: "piece1", Net: "N", Layer: 1, X1: 0, Y1: 6, X2: 15, Y2: 3, W: 8},
		{ID: "piece2", Net: "N", Layer: 1, X1: 15, Y1: 3, X2: 30, Y2: 0, W: 8},
		{ID: "route", Net: "N", Layer: 1, X1: 0, Y1: 0, X2: 100, Y2: 0, W: 8},
		{ID: "locked", Net: "N", Layer: 1, X1: 0, Y1: 6, X2: 30, Y2: 0, W: 8, Locked: true},
	}
	if got := teardropRemovals(tracks, planned); len(got) != 2 || got[0] != "piece1" || got[1] != "piece2" {
		t.Fatalf("got %v", got)
	}
}

func TestPlanTeardropsStopBeforeJunction(t *testing.T) {
	pad := pePad{Ref: "U2.1", Net: "CS", Layer: 1, X: 569, Y: -135, W: 88.6, H: 24.8, Kind: "OVAL"}
	in := peTrack{ID: "in", Net: "CS", Layer: 1, X1: 569, Y1: -135, X2: 515, Y2: -135, W: 8}
	on := peTrack{ID: "on", Net: "CS", Layer: 1, X1: 515, Y1: -135, X2: 460, Y2: -135, W: 8}
	branch := peTrack{ID: "br", Net: "CS", Layer: 1, X1: 515, Y1: -135, X2: 515, Y2: -156, W: 8}
	lines, skips := planTeardrops([]peTrack{in, on, branch}, []pePad{pad}, nil, 6.5, nil)
	for _, l := range lines {
		if l.To[0] < 515+2.4 {
			t.Fatalf("teardrop tip at or past the T-junction: %+v", l)
		}
	}
	_ = skips
}
