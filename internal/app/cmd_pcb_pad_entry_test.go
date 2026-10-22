package app

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
	}
	if got, err := teardropRemovals(tracks, planned); err != nil || len(got) != 2 || got[0] != "piece1" || got[1] != "piece2" {
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

// Synthetic mil geometry covering the planning contract; not live host evidence.
func teardropEditFixture() ([]peTrack, []pePad, []peLine) {
	pads := []pePad{{Ref: "C1.1", Net: "N", Layer: 1, W: 20, H: 24, Kind: "RECT"}}
	tracks := []peTrack{{ID: "main", Net: "N", Layer: 1, X2: 100, W: 8}}
	lines, _ := planTeardrops(tracks, pads, nil, 6.5, nil)
	for i, l := range lines {
		tracks = append(tracks, peTrack{ID: fmt.Sprintf("tear%d", i), Net: l.Net, Layer: l.Layer, X1: l.From[0], Y1: l.From[1], X2: l.To[0], Y2: l.To[1], W: l.Width})
	}
	return tracks, pads, lines
}

func TestTeardropRemovalsFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func([]peTrack) []peTrack
	}{
		{"missing", func(ts []peTrack) []peTrack { return ts[:2] }},
		{"locked", func(ts []peTrack) []peTrack { ts[1].Locked = true; return ts }},
		{"width", func(ts []peTrack) []peTrack { ts[1].W++; return ts }},
		{"offset", func(ts []peTrack) []peTrack { ts[1].Y1 += 0.2; ts[1].Y2 += 0.2; return ts }},
		{"partial", func(ts []peTrack) []peTrack {
			ts[1].X2 = (ts[1].X1 + ts[1].X2) / 2
			ts[1].Y2 = (ts[1].Y1 + ts[1].Y2) / 2
			return ts
		}},
		{"overlap", func(ts []peTrack) []peTrack { dup := ts[1]; dup.ID = "duplicate"; return append(ts, dup) }},
		{"layer", func(ts []peTrack) []peTrack { ts[1].Layer = 2; return ts }},
		{"net", func(ts []peTrack) []peTrack { ts[1].Net = "other"; return ts }},
		{"duplicate-id", func(ts []peTrack) []peTrack { ts[2].ID = ts[1].ID; return ts }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts, _, ls := teardropEditFixture()
			ids, err := teardropRemovals(tc.edit(ts), ls)
			if err == nil || len(ids) != 0 {
				t.Fatalf("ids=%v err=%v", ids, err)
			}
		})
	}
	ts, _, ls := teardropEditFixture()
	ts[1].X1, ts[1].X2 = ts[1].X2, ts[1].X1
	ts[1].Y1, ts[1].Y2 = ts[1].Y2, ts[1].Y1
	if ids, err := teardropRemovals(ts, ls); err != nil || !reflect.DeepEqual(ids, []string{"tear0", "tear1"}) {
		t.Fatalf("reverse match: %v %v", ids, err)
	}
}

func TestModifyTeardropsOnlyRebuildsOldTargets(t *testing.T) {
	ts, ps, old := teardropEditFixture()
	// Another entry could get a teardrop, but must not leak into the modify plan.
	ts = append(ts, peTrack{ID: "unrelated", Net: "U", Layer: 2, X1: 200, Y1: 200, X2: 300, Y2: 200, W: 8})
	ps = append(ps, pePad{Ref: "C2.1", Net: "U", Layer: 2, X: 200, Y: 200, W: 20, H: 24, Kind: "RECT"})
	ids, ls, err := modifyTeardrops(ts, ps, nil, old, 6.5, 0.8, 0.8)
	if err != nil || !reflect.DeepEqual(ids, []string{"tear0", "tear1"}) || len(ls) != 2 {
		t.Fatalf("%v %+v %v", ids, ls, err)
	}
	for i, l := range ls {
		if l.Reason != "teardrop C1.1" || l.Width != 8 || l.To[0] >= old[i].To[0] || math.Abs(l.From[1]) >= math.Abs(old[i].From[1]) {
			t.Fatalf("not resized: %+v", ls)
		}
	}
	// The modified plan can itself be deleted, keeping the main track.
	updated := []peTrack{ts[0]}
	for i, l := range ls {
		updated = append(updated, peTrack{ID: fmt.Sprintf("new%d", i), Net: l.Net, Layer: l.Layer, W: l.Width, X1: l.From[0], Y1: l.From[1], X2: l.To[0], Y2: l.To[1]})
	}
	if ids, err := teardropRemovals(updated, ls); err != nil || len(ids) != 2 {
		t.Fatalf("delete modified: %v %v", ids, err)
	}
	// Scales apply to the default geometry, not cumulatively to the last run.
	_, again, err := modifyTeardrops(updated, ps, nil, ls, 6.5, 0.8, 0.8)
	if err != nil || !reflect.DeepEqual(again, ls) {
		t.Fatalf("repeat drift: %+v %v", again, err)
	}
}

func TestModifyTeardropsRejectsUnsafeReplacement(t *testing.T) {
	for _, tc := range []struct {
		name         string
		length, base float64
	}{
		{"zero", 0, 1}, {"negative", -1, 1}, {"nan", math.NaN(), 1}, {"inf", 1, math.Inf(1)}, {"base-too-large", 1, 2}, {"too-short", 0.001, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts, ps, ls := teardropEditFixture()
			ids, newLines, err := modifyTeardrops(ts, ps, nil, ls, 6.5, tc.length, tc.base)
			if err == nil || len(ids) > 0 || len(newLines) > 0 {
				t.Fatalf("%v %v %v", ids, newLines, err)
			}
		})
	}
	ts, ps, ls := teardropEditFixture()
	ts = append(ts, peTrack{ID: "foreign", Net: "F", Layer: 1, X1: 10, Y1: 10, X2: 80, Y2: 10, W: 8})
	if ids, _, err := modifyTeardrops(ts, ps, nil, ls, 6.5, 1, 1); err == nil || len(ids) > 0 {
		t.Fatalf("clearance failure emitted deletions: %v %v", ids, err)
	}
}

func writeTeardropFixtureFiles(t *testing.T) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	ts, _, ls := teardropEditFixture()
	b := boardSnapshot{ComponentsAvailability: "available", Components: []boardComp{{Designator: "C1", Pads: []boardPad{{Number: "1", Net: "N", Layer: 1, W: 20, H: 24, Shape: []any{"RECT", 20, 24}}}}}, Copper: &boardCopperSnapshot{Availability: map[string]string{"routing": "available", "vias": "available"}}}
	for _, v := range ts {
		b.Copper.Lines = append(b.Copper.Lines, map[string]any{"primitiveId": v.ID, "net": v.Net, "layer": v.Layer, "lineWidth": v.W, "startX": v.X1, "startY": v.Y1, "endX": v.X2, "endY": v.Y2, "locked": false})
	}
	board, plan := filepath.Join(dir, "board.json"), filepath.Join(dir, "old.json")
	if err := peWrite(board, b); err != nil {
		t.Fatal(err)
	}
	if err := peWrite(plan, map[string]any{"units": "mil", "lines": ls}); err != nil {
		t.Fatal(err)
	}
	return dir, board, plan
}

func TestTeardropEditCommands(t *testing.T) {
	for _, modify := range []bool{false, true} {
		t.Run(fmt.Sprint(modify), func(t *testing.T) {
			dir, board, plan := writeTeardropFixtureFiles(t)
			out := filepath.Join(dir, "next.json")
			cmd := newPcbTeardropEditCmd(io.Discard, modify)
			args := []string{"--board", board, "--from", plan, "--out", out, "--apply-project", "test-project", "--apply-doc", "PCB1"}
			if modify {
				args = append(args, "--length-scale", "0.8", "--base-scale", "0.8")
			}
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(dir, "next.playbook.json"))
			if err != nil {
				t.Fatal(err)
			}
			var p struct {
				RequireFullExecution bool              `json:"requireFullExecution"`
				Meta                 map[string]string `json:"meta"`
				Steps                []struct {
					Action  string `json:"action"`
					Payload struct {
						IDs []string `json:"primitiveIds"`
					} `json:"payload"`
				} `json:"steps"`
			}
			if err = json.Unmarshal(raw, &p); err != nil {
				t.Fatal(err)
			}
			expected := 2
			if modify {
				expected = 4
			}
			if len(p.Steps) != expected || !p.RequireFullExecution || p.Meta["project"] != "test-project" || p.Meta["doc"] != "PCB1" || p.Steps[0].Action != "pcb.route.delete" || p.Steps[expected-1].Action != "pcb.save" || !reflect.DeepEqual(p.Steps[0].Payload.IDs, []string{"tear0", "tear1"}) {
				t.Fatalf("bad playbook: %s", raw)
			}
			if modify {
				for _, s := range p.Steps[1:3] {
					if s.Action != "pcb.line.create" {
						t.Fatal(s)
					}
				}
				if _, err = readTeardropPlan(out); err != nil {
					t.Fatal(err)
				}
			}
			// A re-run must not overwrite the existing audit files.
			cmd = newPcbTeardropEditCmd(io.Discard, modify)
			cmd.SetArgs(args)
			if err = cmd.Execute(); err == nil {
				t.Fatal("overwrote output")
			}
		})
	}
}

func TestTeardropEditCommandFailureLeavesNoPlaybook(t *testing.T) {
	dir, board, plan := writeTeardropFixtureFiles(t)
	out := filepath.Join(dir, "next.json")
	raw, err := os.ReadFile(board)
	if err != nil {
		t.Fatal(err)
	}
	var b boardSnapshot
	if err = json.Unmarshal(raw, &b); err != nil {
		t.Fatal(err)
	}
	// Lines[0] is the main track; lock one teardrop line instead.
	b.Copper.Lines[1].(map[string]any)["locked"] = true
	if err := peWrite(board, b); err != nil {
		t.Fatal(err)
	}
	cmd := newPcbTeardropEditCmd(io.Discard, false)
	cmd.SetArgs([]string{"--board", board, "--from", plan, "--out", out})
	if err := cmd.Execute(); err == nil {
		t.Fatal("accepted locked line")
	}
	for _, p := range []string{out, filepath.Join(dir, "next.playbook.json")} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("output on failure: %s", p)
		}
	}
	if _, err := teardropOutputPaths(plan, board, plan); err == nil {
		t.Fatal("overwrite old plan")
	}
}

func TestTeardropLegacyRemoveUsesSameChecks(t *testing.T) {
	dir, board, plan := writeTeardropFixtureFiles(t)
	for _, cmd := range newPcbPadEntryCmds(io.Discard) {
		if cmd.Name() == "teardrop-plan" {
			cmd.SetArgs([]string{"--board", board, "--remove-from", plan, "--out", filepath.Join(dir, "delete.json")})
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("legacy command missing")
}

func TestReadTeardropPlanRejectsWrongInput(t *testing.T) {
	_, _, ls := teardropEditFixture()
	for _, p := range []any{map[string]any{"units": "mm", "lines": ls}, map[string]any{"units": "mil", "lines": ls[:1]}, map[string]any{"units": "mil", "steps": []any{}}, map[string]any{"units": "mil", "lines": append(ls, ls...)}} {
		file := filepath.Join(t.TempDir(), "plan.json")
		if err := peWrite(file, p); err != nil {
			t.Fatal(err)
		}
		if _, err := readTeardropPlan(file); err == nil {
			t.Fatalf("accepted %+v", p)
		}
	}
}

func TestModifyTeardropsRejectsCollapsedBaseAndParentWidthDrift(t *testing.T) {
	ts, ps, ls := teardropEditFixture()
	if _, _, err := modifyTeardrops(ts, ps, nil, ls, 6.5, 1, 1e-9); err == nil {
		t.Fatal("accepted collapsed base")
	}
	ts[0].W = 9
	if _, _, err := modifyTeardrops(ts, ps, nil, ls, 6.5, 1, 1); err == nil {
		t.Fatal("silently changed line width")
	}
}

func TestModifyTeardropsAfterHostSplitsMainAndTeardrop(t *testing.T) {
	ts, ps, ls := teardropEditFixture()
	// The host splits the main track at both tips and one teardrop line into pieces.
	ts = ts[1:]
	joints := []float64{0, ls[1].To[0], ls[0].To[0], 100}
	for i := 0; i < len(joints)-1; i++ {
		ts = append(ts, peTrack{ID: fmt.Sprintf("main%d", i), Net: "N", Layer: 1, W: 8, X1: joints[i], X2: joints[i+1]})
	}
	edge := ts[0]
	mx, my := (edge.X1+edge.X2)/2, (edge.Y1+edge.Y2)/2
	ts[0].X2, ts[0].Y2 = mx, my
	edge.X1, edge.Y1, edge.ID = mx, my, "split"
	ts = append(ts, edge)
	ids, newLines, err := modifyTeardrops(ts, ps, nil, ls, 6.5, 0.8, 0.8)
	if err != nil || len(ids) != 3 || len(newLines) != 2 {
		t.Fatalf("split rebuild: %v %+v %v", ids, newLines, err)
	}
	for _, id := range ids {
		if strings.HasPrefix(id, "main") {
			t.Fatal("deleted parent", id)
		}
	}
}

func TestTeardropClearanceUsesEitherRule(t *testing.T) {
	for _, tc := range []struct {
		rules *boardRules
		want  float64
	}{
		{nil, 6.5},
		{&boardRules{}, 6.5},
		{&boardRules{ClearanceMil: 8}, 8.5},
		{&boardRules{ClearanceMil: 6, ClearanceTrackTrackMil: 7}, 7.5},
	} {
		if got := teardropClearance(&boardSnapshot{Rules: tc.rules}, 6.5); got != tc.want {
			t.Fatalf("%+v: got %v want %v", tc.rules, got, tc.want)
		}
	}
}
