package app

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPcbEnclosurePlanAndBuildFromBoardSnapshot(t *testing.T) {
	dir := t.TempDir()
	board := `{"outline":{"source":"polygon","bbox":{"minX":0,"minY":0,"maxX":1000,"maxY":800},
	  "points":[[0,0],[1000,0],[1000,800],[0,800]]},
	  "components":[
	    {"primitiveId":"a","designator":"U1","layer":1,"x":500,"y":400,"bbox":{"minX":400,"minY":300,"maxX":600,"maxY":500},"pads":[]},
	    {"primitiveId":"b","designator":"J1","layer":1,"x":-20,"y":400,"bbox":{"minX":-60,"minY":300,"maxX":150,"maxY":500},"pads":[]},
	    {"primitiveId":"c","designator":"H1","layer":1,"footprint":{"name":"HOLE-M2"},"x":850,"y":650,"bbox":{"minX":800,"minY":600,"maxX":900,"maxY":700},"pads":[]}],
	  "semanticSha256":"abc"}`
	models := `{"ok":true,"result":{"components":[{"designator":"U1","model":{"name":"SOIC-8_L5.3-W5.3-H1.9"},"height":null},
	  {"designator":"J1","model":{"name":"USB-C"},"height":"3.2mm"}]}}`
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	boardPath, modelsPath, spec := write("board.json", board), write("models.json", models), filepath.Join(dir, "spec.json")
	run := func(c interface {
		SetArgs([]string)
		Execute() error
	}, args ...string) error {
		c.SetArgs(args)
		return c.Execute()
	}
	var out bytes.Buffer
	if err := run(newPcbEnclosurePlanCmd(&out, io.Discard), "--board", boardPath, "--models", modelsPath, "--resolution", "0.2", "--height", "U1=1.9", "--out", spec); err != nil {
		t.Fatal(err)
	}
	var planned map[string]any
	data, _ := os.ReadFile(spec)
	_ = json.Unmarshal(data, &planned)
	if len(planned["pillars"].([]any)) != 1 || len(planned["features"].([]any)) != 1 {
		t.Fatalf("expected 1 pillar and the J1 side opening: %s", data)
	}
	if err := run(newPcbEnclosurePlanCmd(io.Discard, io.Discard), "--board", boardPath, "--out", spec); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Fatalf("existing spec overwritten: %v", err)
	}
	outDir := filepath.Join(dir, "enc")
	if err := run(newPcbEnclosureBuildCmd(&out, io.Discard), "--spec", spec, "--out", outDir); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"bottom.stl", "top.stl", "manifest.json", "preview.png"} {
		if st, err := os.Stat(filepath.Join(outDir, f)); err != nil || st.Size() == 0 {
			t.Fatalf("%s missing: %v", f, err)
		}
	}
	// Raise U1 above the lid: build must fail and leave only the manifest.
	bad := strings.Replace(string(data), `"height": 1.9`, `"height": 40`, 1)
	if bad == string(data) {
		t.Fatal("fixture did not contain U1 height")
	}
	badSpec, badOut := write("bad.json", bad), filepath.Join(dir, "bad")
	if err := run(newPcbEnclosureBuildCmd(io.Discard, io.Discard), "--spec", badSpec, "--out", badOut); err == nil {
		t.Fatal("interfering spec built")
	}
	if _, err := os.Stat(filepath.Join(badOut, "bottom.stl")); err == nil {
		t.Fatal("failed build wrote STL")
	}
}

func TestPcbEnclosureSlideWithManualPillarAndStep(t *testing.T) {
	dir := t.TempDir()
	board := filepath.Join(dir, "board.json")
	if err := os.WriteFile(board, []byte(`{"outline":{"source":"polygon","bbox":{"minX":0,"minY":0,"maxX":1200,"maxY":800},
	  "points":[[0,0],[1200,0],[1200,800],[0,800]]},
	  "components":[{"primitiveId":"a","designator":"U1","layer":1,"x":600,"y":400,"bbox":{"minX":500,"minY":300,"maxX":700,"maxY":500},"pads":[]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	spec, out := filepath.Join(dir, "spec.json"), filepath.Join(dir, "enc")
	plan := newPcbEnclosurePlanCmd(io.Discard, io.Discard)
	plan.SetArgs([]string{"--board", board, "--type", "slide", "--slide-side", "y+", "--pillar", "3,3,M2", "--resolution", "0.2", "--out", spec})
	if err := plan.Execute(); err != nil {
		t.Fatal(err)
	}
	build := newPcbEnclosureBuildCmd(io.Discard, io.Discard)
	build.SetArgs([]string{"--spec", spec, "--out", out, "--step"})
	if err := build.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"box.stl", "lid.stl", "box.step", "lid.step", "preview.png"} {
		if st, err := os.Stat(filepath.Join(out, f)); err != nil || st.Size() == 0 {
			t.Fatalf("%s missing: %v", f, err)
		}
	}
	if _, err := parsePillarFlag("3", "M3"); err == nil {
		t.Fatal("bad --pillar accepted")
	}
}
