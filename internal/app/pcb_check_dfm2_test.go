package app

import "testing"

// ── silk-over-pad (§11.2) ────────────────────────────────────────────────────

func TestFindSilkOverPad(t *testing.T) {
	pads := []pcbPadP{
		{Designator: "R1", Number: "1", Net: "N1", Layer: 1, X: 0, Y: 0},     // top pad under top silk
		{Designator: "R2", Number: "1", Net: "N2", Layer: 2, X: 0, Y: 0},     // bottom pad — top silk can't hit it
		{Designator: "R3", Number: "1", Net: "N3", Layer: 1, X: 500, Y: 500}, // far away
	}
	silk := []pcbSilkText{
		{ID: "s1", Kind: "attribute", Key: "Designator", Text: "R1", Layer: 3, X: 5, Y: 5},
		{ID: "s2", Kind: "string", Text: "OK", Layer: 3, X: 300, Y: 300}, // clear
	}
	out := findSilkOverPad(silk, pads)
	if len(out) != 1 {
		t.Fatalf("silk-over-pad = %d, want 1 (%+v)", len(out), out)
	}
	if out[0].Designator != "R1" || out[0].Primitives[0] != "s1" {
		t.Errorf("wrong target: %+v", out[0])
	}
}

// A through-hole (multi-layer) pad is exposed on BOTH silk sides.
func TestFindSilkOverPad_ThroughHole(t *testing.T) {
	pads := []pcbPadP{{Designator: "J1", Number: "1", Net: "N1", Layer: 11, X: 0, Y: 0}}
	silk := []pcbSilkText{{ID: "s1", Kind: "string", Text: "TXT", Layer: 4, X: 0, Y: 0}}
	if out := findSilkOverPad(silk, pads); len(out) != 1 {
		t.Fatalf("through-hole pad under bottom silk = %d findings, want 1", len(out))
	}
}

// 90°-rotated text swaps its extents: a pad beside the (now tall) box that would
// have been inside the unrotated wide box must NOT be flagged.
func TestFindSilkOverPad_Rotation(t *testing.T) {
	// 10-char text: unrotated half-width 120mil; rotated 90° half-width becomes 20.
	pads := []pcbPadP{{Designator: "R1", Number: "1", Net: "N1", Layer: 1, X: 100, Y: 0}}
	silk := []pcbSilkText{{ID: "s1", Kind: "string", Text: "ABCDEFGHIJ", Layer: 3, X: 0, Y: 0, Rotation: 90}}
	if out := findSilkOverPad(silk, pads); len(out) != 0 {
		t.Fatalf("rotated-away text flagged: %+v", out)
	}
}

// ── decap-too-far (§3.1) ─────────────────────────────────────────────────────

func TestFindDecapTooFar(t *testing.T) {
	pads := []pcbPadP{
		// U1: 3V3 pin at origin.
		{Designator: "U1", Number: "1", Net: "3V3", Layer: 1, X: 0, Y: 0},
		{Designator: "U1", Number: "2", Net: "GND", Layer: 1, X: 0, Y: 50},
		// C1: proper decap, 40mil away — OK.
		{Designator: "C1", Number: "1", Net: "3V3", Layer: 1, X: 40, Y: 0},
		{Designator: "C1", Number: "2", Net: "GND", Layer: 1, X: 80, Y: 0},
		// C2: decap parked 400mil away — flagged.
		{Designator: "C2", Number: "1", Net: "3V3", Layer: 1, X: 400, Y: 0},
		{Designator: "C2", Number: "2", Net: "GND", Layer: 1, X: 440, Y: 0},
		// C3: rail with no IC pad (VIN) — bulk cap, skipped.
		{Designator: "C3", Number: "1", Net: "VIN", Layer: 1, X: 900, Y: 0},
		{Designator: "C3", Number: "2", Net: "GND", Layer: 1, X: 940, Y: 0},
		// C4: signal-signal cap (AC coupling) — not a decap, skipped.
		{Designator: "C4", Number: "1", Net: "SIG_A", Layer: 1, X: 900, Y: 900},
		{Designator: "C4", Number: "2", Net: "SIG_B", Layer: 1, X: 940, Y: 900},
	}
	out := findDecapTooFar(pads)
	if len(out) != 1 {
		t.Fatalf("decap-too-far = %d, want 1 (%+v)", len(out), out)
	}
	if out[0].Designator != "C2" || out[0].Net != "3V3" {
		t.Errorf("wrong offender: %+v", out[0])
	}
}

// ── via-in-pad (§2.3) ────────────────────────────────────────────────────────

func TestFindViaInPad(t *testing.T) {
	pads := []pcbPadP{
		{Designator: "U1", Number: "1", Net: "GND", Layer: 1, X: 0, Y: 0},
		{Designator: "U1", Number: "2", Net: "3V3", Layer: 1, X: 100, Y: 0},
	}
	vias := []pcbViaP{
		{ID: "v1", Net: "GND", X: 2, Y: 2, Hole: 12, Dia: 24},  // ON the GND pad → flagged
		{ID: "v2", Net: "GND", X: 40, Y: 0, Hole: 12, Dia: 24}, // offset dog-bone → OK
		{ID: "v3", Net: "3V3", X: 0, Y: 0, Hole: 12, Dia: 24},  // cross-net on pad — clearance rule's case, not ours
	}
	out := findViaInPad(vias, pads)
	if len(out) != 1 {
		t.Fatalf("via-in-pad = %d, want 1 (%+v)", len(out), out)
	}
	if out[0].Primitives[0] != "v1" {
		t.Errorf("wrong via: %+v", out[0])
	}
}

// Real pad extents drive via-in-pad: a via 20mil from a BIG pad's center (60×60,
// half=30) is ON it; the same via next to a default-size pad is not.
func TestFindViaInPad_RealExtent(t *testing.T) {
	vias := []pcbViaP{{ID: "v1", Net: "GND", X: 20, Y: 0, Hole: 12, Dia: 24}}
	big := []pcbPadP{{Designator: "U1", Number: "EP", Net: "GND", Layer: 1, X: 0, Y: 0, W: 60, H: 60}}
	if out := findViaInPad(vias, big); len(out) != 1 {
		t.Fatalf("via 20mil into a 60mil pad = %d findings, want 1", len(out))
	}
	small := []pcbPadP{{Designator: "R1", Number: "1", Net: "GND", Layer: 1, X: 0, Y: 0, W: 16, H: 16}}
	if out := findViaInPad(vias, small); len(out) != 0 {
		t.Fatalf("via 20mil from an 16mil pad must be clear, got %+v", out)
	}
}

// Real fontSize drives silk-over-pad: a small 10mil label near (not on) the pad
// clears; the same label at 60mil font reaches the pad.
func TestFindSilkOverPad_FontSize(t *testing.T) {
	pads := []pcbPadP{{Designator: "R1", Number: "1", Net: "N1", Layer: 1, X: 40, Y: 0, W: 16, H: 16}}
	small := []pcbSilkText{{ID: "s1", Kind: "string", Text: "AB", Layer: 3, X: 0, Y: 0, FontSize: 10}}
	if out := findSilkOverPad(small, pads); len(out) != 0 {
		t.Fatalf("10mil label must clear the pad, got %+v", out)
	}
	huge := []pcbSilkText{{ID: "s1", Kind: "string", Text: "AB", Layer: 3, X: 0, Y: 0, FontSize: 60}}
	if out := findSilkOverPad(huge, pads); len(out) != 1 {
		t.Fatalf("60mil label reaches the pad = %d findings, want 1", len(out))
	}
}

// ── copper-near-edge (§5.1) ──────────────────────────────────────────────────

func TestFindCopperNearEdge(t *testing.T) {
	outline := &layoutBBox{MinX: 0, MinY: 0, MaxX: 1000, MaxY: 1000}
	tracks := []pcbTrack{
		// endpoint 4mil from the left edge (width 10 → copper edge at -1) → flagged
		{ID: "t1", Net: "SIG1", Layer: 1, X1: 4, Y1: 500, X2: 300, Y2: 500, Width: 10},
		// comfortably interior → OK
		{ID: "t2", Net: "SIG2", Layer: 1, X1: 500, Y1: 500, X2: 600, Y2: 500, Width: 10},
		// net-less (board outline segment itself) → skipped
		{ID: "t3", Net: "", Layer: 1, X1: 0, Y1: 0, X2: 1000, Y2: 0, Width: 10},
	}
	vias := []pcbViaP{
		{ID: "v1", Net: "GND", X: 995, Y: 500, Hole: 12, Dia: 24}, // 5mil to edge minus radius → flagged
		{ID: "v2", Net: "GND", X: 500, Y: 500, Hole: 12, Dia: 24}, // interior → OK
	}
	out := findCopperNearEdge(tracks, vias, outline, 8)
	if len(out) != 2 { // aggregated per net: SIG1 + GND
		t.Fatalf("copper-near-edge = %d findings, want 2 (%+v)", len(out), out)
	}
	nets := map[string]bool{}
	for _, f := range out {
		nets[f.Net] = true
	}
	if !nets["SIG1"] || !nets["GND"] {
		t.Errorf("expected SIG1+GND, got %v", nets)
	}
	// nil outline → rule disabled, never panics
	if out := findCopperNearEdge(tracks, vias, nil, 8); out != nil {
		t.Errorf("nil outline must disable the rule, got %+v", out)
	}
}

// ── fiducial-missing (§9) ────────────────────────────────────────────────────

func TestFindFiducialMissing(t *testing.T) {
	// SMT-scale board (40 top pads), no fiducials → INFO.
	var pads []pcbPadP
	for i := 0; i < 40; i++ {
		pads = append(pads, pcbPadP{Designator: "U1", Number: "p", Net: "N", Layer: 1, X: float64(i), Y: 0})
	}
	out := findFiducialMissing(pads)
	if len(out) != 1 || out[0].Level != "INFO" {
		t.Fatalf("fiducial-missing = %+v, want 1 INFO", out)
	}
	// With 3 fiducials → clean.
	withFids := append(pads,
		pcbPadP{Designator: "FID1", Layer: 1, X: 0, Y: 0},
		pcbPadP{Designator: "FID2", Layer: 1, X: 100, Y: 0},
		pcbPadP{Designator: "FID3", Layer: 1, X: 0, Y: 100},
	)
	if out := findFiducialMissing(withFids); len(out) != 0 {
		t.Fatalf("3 fiducials must satisfy the rule, got %+v", out)
	}
	// Small hand-solder board (few pads) → rule silent.
	if out := findFiducialMissing(pads[:10]); len(out) != 0 {
		t.Fatalf("small board must not be flagged, got %+v", out)
	}
}

// The full-core clean board stays clean with the new rules wired in (no false
// positives from a well-formed layout).
func TestPcbCheck_DFM2_CleanBoardStillClean(t *testing.T) {
	pads := []pcbPadP{
		{Designator: "U1", Number: "1", Net: "NET1", Layer: 1, X: 0, Y: 0},
		{Designator: "U1", Number: "2", Net: "NET1", Layer: 1, X: 100, Y: 100},
	}
	tracks := []pcbTrack{
		{ID: "t1", Net: "NET1", Layer: 1, X1: 0, Y1: 0, X2: 100, Y2: 0, Width: 10},
		{ID: "t2", Net: "NET1", Layer: 1, X1: 100, Y1: 0, X2: 100, Y2: 100, Width: 10},
	}
	rep := analyzePcbCheck(pads, tracks, nil, 0)
	if !rep.Passed || rep.Summary.Total != 0 {
		t.Fatalf("expected clean board, got %d findings: %+v", rep.Summary.Total, rep.Findings)
	}
}

func TestFindSilkOffBoardFlagsOnlyVisibleSilkOutsideTheOutline(t *testing.T) {
	yes, no := true, false
	board := pcbRect{MinX: -240, MinY: -1600, MaxX: 940, MaxY: -343}
	silk := []pcbSilkText{
		// Live 2026-10-13: U1 designator above the board's top edge.
		{ID: "u1", Text: "U1", Layer: silkTopLayer, X: 0, Y: -337.7, Visible: &yes, BBox: &pcbRect{MinX: 0, MinY: -337.7, MaxX: 52, MaxY: -292.7}},
		{ID: "c1", Text: "C1", Layer: silkTopLayer, X: -200, Y: -761, Visible: &yes, BBox: &pcbRect{MinX: -200, MinY: -761, MaxX: -158, MaxY: -716}},
		{ID: "hidden", Text: "C0805", Layer: silkTopLayer, X: -1068, Y: -504, Visible: &no},
		{ID: "unknown", Text: "?", Layer: silkTopLayer, X: -1068, Y: -504},
	}
	got := findSilkOffBoard(silk, board)
	if len(got) != 1 || got[0].Designator != "U1" || got[0].Type != "silk-off-board" {
		t.Fatalf("want only U1 flagged, got %+v", got)
	}
}

func TestSilkOverPadReproducesDemoNeighbourLabelAndSkipsHiddenText(t *testing.T) {
	yes, no := true, false
	// Demo r1 (2026-10-13), measured: R1 centred at y -890 (0402 pads 22.3×21.3),
	// C2's designator box 45 mil tall sitting just above C2 at y -970. layout-lint
	// reported 0 overlap; the label touched R1's pads by ~3.4 mil.
	pads := []pcbPadP{
		{Designator: "R1", Number: "1", Layer: pcbSideTop, X: -137, Y: -890, W: 22.3, H: 21.3},
		{Designator: "R1", Number: "2", Layer: pcbSideTop, X: -103, Y: -890, W: 22.3, H: 21.3},
	}
	visible := pcbSilkText{ID: "c2", Kind: "attribute", Key: "Designator", Text: "C2", Layer: silkTopLayer, Visible: &yes,
		BBox: &pcbRect{MinX: -157.4, MinY: -942.3, MaxX: -104.2, MaxY: -897.3}}
	if got := findSilkOverPad([]pcbSilkText{visible}, pads); len(got) != 1 || got[0].Designator != "R1" {
		t.Fatalf("demo neighbour label must be reported on R1, got %+v", got)
	}
	hidden := visible
	hidden.ID, hidden.Visible = "hidden-footprint", &no
	if got := findSilkOverPad([]pcbSilkText{hidden}, pads); len(got) != 0 {
		t.Fatalf("hidden attribute text is not printed and must not be judged, got %+v", got)
	}
}

func TestViaInPadUsesPadRectangle(t *testing.T) {
	pad := pcbPadP{Designator: "U2", Number: "4", Net: "GND", X: 569, Y: 15, W: 88.6, H: 24.8}
	clear := pcbViaP{ID: "dogbone", Net: "GND", X: 569, Y: 52, Dia: 24}
	on := pcbViaP{ID: "onpad", Net: "GND", X: 600, Y: 20, Dia: 24}
	if got := findViaInPad([]pcbViaP{clear}, []pcbPadP{pad}); len(got) != 0 {
		t.Fatalf("dog-bone via 12.5 mil clear of the pad flagged: %+v", got)
	}
	if got := findViaInPad([]pcbViaP{on}, []pcbPadP{pad}); len(got) != 1 {
		t.Fatalf("via on the pad not flagged: %+v", got)
	}
}

func TestTeardropLinesExemptFromCornerAndGridRules(t *testing.T) {
	pad := pcbPadP{Designator: "C1", Number: "1", Net: "N", Layer: 1, X: 0, Y: 0, W: 19.7, H: 21.3}
	tearA := pcbTrack{ID: "a", Net: "N", Layer: 1, X1: 5.85, Y1: 6.15, X2: 33.85, Y2: 0, Width: 8}
	tearB := pcbTrack{ID: "b", Net: "N", Layer: 1, X1: 5.85, Y1: -6.15, X2: 32.85, Y2: 0, Width: 8}
	stray := pcbTrack{ID: "s", Net: "N", Layer: 1, X1: 100, Y1: 0, X2: 130, Y2: 17, Width: 8} // real free-angle routing
	// the host splits the reinforced track at the tips (live): pieces end there
	pieceA := pcbTrack{ID: "p1", Net: "N", Layer: 1, X1: 0, Y1: 0, X2: 32.85, Y2: 0, Width: 8}
	pieceB := pcbTrack{ID: "p2", Net: "N", Layer: 1, X1: 32.85, Y1: 0, X2: 33.85, Y2: 0, Width: 8}
	pieceC := pcbTrack{ID: "p3", Net: "N", Layer: 1, X1: 33.85, Y1: 0, X2: 100, Y2: 0, Width: 8}
	got := withoutTeardrops([]pcbTrack{pieceA, pieceB, pieceC, tearA, tearB, stray}, []pcbPadP{pad})
	if len(got) != 4 || got[3].ID != "s" {
		t.Fatalf("kept %+v", got)
	}
	if f := findNonOrthogonal(got); len(f) != 1 || f[0].Primitives[0] != "s" {
		t.Fatalf("non-orthogonal findings %+v", f)
	}
	if f := findAcuteAngles(got, []pcbPadP{pad}); len(f) != 0 {
		t.Fatalf("acute findings at teardrop tips %+v", f)
	}
}

func TestThinTeardropPairStillExempt(t *testing.T) {
	pad := pcbPadP{Designator: "U2", Number: "8", Net: "V", Layer: 1, X: 291, Y: -135, W: 88.6, H: 24.8}
	in := pcbTrack{ID: "in", Net: "V", Layer: 1, X1: 291, Y1: -135, X2: 200, Y2: -135, Width: 17.7}
	a := pcbTrack{ID: "a", Net: "V", Layer: 1, X1: 251.5, Y1: -132.5, X2: 205, Y2: -135, Width: 17.7}
	b := pcbTrack{ID: "b", Net: "V", Layer: 1, X1: 251.5, Y1: -137.5, X2: 206, Y2: -135, Width: 17.7}
	got := withoutTeardrops([]pcbTrack{in, a, b}, []pcbPadP{pad})
	if len(got) != 1 || got[0].ID != "in" {
		t.Fatalf("kept %+v", got)
	}
}
