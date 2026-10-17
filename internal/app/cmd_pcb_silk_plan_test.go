package app

import "testing"

func spTestOpts(rot ...float64) spOptions {
	return spOptions{PadGap: 3, BodyGap: 3, LabelGap: 3, EdgeGap: 0, MaxDist: 60, Step: 2, RotPenalty: 25, Rotations: rot}
}

func TestPlanSilkPrefersUprightThenNinety(t *testing.T) {
	part := pcbRect{MinX: 0, MinY: 0, MaxX: 40, MaxY: 20}
	label := spLabel{ID: "d", Ref: "C1", Comp: part, W: 60, H: 45}
	board := pcbRect{MinX: -200, MinY: -200, MaxX: 200, MaxY: 200}
	got, failed := planSilk([]spLabel{label}, nil, map[string]pcbRect{"C1": part}, board, spTestOpts(0, 90))
	if len(failed) != 0 || len(got) != 1 || got[0].Rotation != 0 {
		t.Fatalf("open board: got %+v failed %v", got, failed)
	}
	// a short board and close neighbours leave only a 90° slot on the right
	board = pcbRect{MinX: -100, MinY: -40, MaxX: 140, MaxY: 60}
	bodies := map[string]pcbRect{"C1": part, "L": {MinX: -100, MinY: -40, MaxX: -50, MaxY: 60}, "R": {MinX: 95, MinY: -40, MaxX: 140, MaxY: 60}}
	got, failed = planSilk([]spLabel{label}, nil, bodies, board, spTestOpts(0, 90))
	if len(failed) != 0 || got[0].Rotation != 90 || got[0].Box.MinX < part.MaxX {
		t.Fatalf("tight board: got %+v failed %v", got, failed)
	}
	if _, failed = planSilk([]spLabel{label}, nil, bodies, board, spTestOpts(0)); len(failed) != 1 {
		t.Fatalf("upright-only should report no slot, failed=%v", failed)
	}
}

func TestPlanSilkAvoidsPadsAndOtherLabels(t *testing.T) {
	part := pcbRect{MinX: 0, MinY: 0, MaxX: 40, MaxY: 20}
	other := spLabel{ID: "o", Ref: "X", Fixed: true, X: 0, Y: 23, W: 60, H: 45, Box: spBox(0, 23, 60, 45, 0)}
	label := spLabel{ID: "d", Ref: "C1", Comp: part, W: 60, H: 45}
	pads := []pcbRect{{MinX: 45, MinY: -10, MaxX: 70, MaxY: 30}}
	got, _ := planSilk([]spLabel{other, label}, pads, map[string]pcbRect{"C1": part}, pcbRect{MinX: -200, MinY: -200, MaxX: 200, MaxY: 200}, spTestOpts(0))
	if len(got) != 1 || spOverlap(got[0].Box, spBox(0, 23, 60, 45, 0), 3) || spOverlap(got[0].Box, pads[0], 3) {
		t.Fatalf("label collides: %+v", got)
	}
}

func TestPlanSilkOwnershipByLabelCentre(t *testing.T) {
	c6 := pcbRect{MinX: -340, MinY: -33, MaxX: -260, MaxY: 13}
	bodies := map[string]pcbRect{"C6": c6, "R1": {MinX: -340, MinY: 17, MaxX: -260, MaxY: 63}, "R2": {MinX: -340, MinY: -83, MaxX: -260, MaxY: -37}}
	label := spLabel{ID: "c6", Ref: "C6", Comp: c6, W: 57, H: 45}
	got, failed := planSilk([]spLabel{label}, nil, bodies, pcbRect{MinX: -600, MinY: -400, MaxX: 0, MaxY: 400}, spTestOpts(0, 90))
	if len(failed) != 0 || len(got) != 1 {
		t.Fatalf("stacked column: got %+v failed %v", got, failed)
	}
}

func TestPlanSilkHintNearestItsOwnPad(t *testing.T) {
	body := pcbRect{MinX: 120, MinY: 440, MaxX: 540, MaxY: 520}
	pad := func(x float64) pcbRect { return pcbRect{MinX: x - 33, MinY: 447, MaxX: x + 33, MaxY: 513} }
	pads := []pcbRect{pad(180), pad(280), pad(380), pad(480)}
	hint := spLabel{Ref: "J1", Comp: pad(280), W: 58, H: 50, Text: "TX", Siblings: []pcbRect{pad(180), pad(380), pad(480)}}
	got, failed := planSilk([]spLabel{hint}, pads, map[string]pcbRect{"J1": body}, pcbRect{MinX: -600, MinY: -470, MaxX: 650, MaxY: 560}, spTestOpts(0))
	if len(failed) != 0 || len(got) != 1 {
		t.Fatalf("got %+v failed %v", got, failed)
	}
	cx := (got[0].Box.MinX + got[0].Box.MaxX) / 2
	if cx < 230 || cx > 330 || got[0].Text != "TX" {
		t.Fatalf("hint not under its own pin: %+v", got[0])
	}
}

func TestSpAnchorConventions(t *testing.T) {
	b := pcbRect{MinX: 10, MinY: 20, MaxX: 70, MaxY: 70}
	if x, y := spAnchor(b, 0, false); x != 10 || y != 20 {
		t.Fatalf("attribute upright anchor %v,%v", x, y)
	}
	if x, y := spAnchor(b, 90, false); x != 70 || y != 20 {
		t.Fatalf("attribute 90° anchor %v,%v", x, y)
	}
	if x, y := spAnchor(b, 0, true); x != 10 || y != 70 {
		t.Fatalf("free string anchor %v,%v (live: pcb.silk.add text hangs below its y)", x, y)
	}
	hint := spLabel{Ref: "J1", Comp: pcbRect{MinX: 0, MinY: 0, MaxX: 40, MaxY: 20}, W: 60, H: 45, Text: "TX", Free: true}
	got, _ := planSilk([]spLabel{hint}, nil, map[string]pcbRect{"J1": hint.Comp}, pcbRect{MinX: -200, MinY: -200, MaxX: 200, MaxY: 200}, spTestOpts(0, 90))
	if len(got) != 1 || got[0].Rotation != 0 || got[0].Y != got[0].Box.MaxY {
		t.Fatalf("free text must be upright with a top-left anchor: %+v", got)
	}
}
