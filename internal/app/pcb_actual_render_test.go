package app

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Synthetic, asymmetric geometry in mil. This is not a private/live board.
func actualRenderFixture(t *testing.T) []byte {
	t.Helper()
	available := true
	zero := 0
	s := boardSnapshot{CapturedAt: "2026-01-02T03:04:05Z", Project: "synthetic",
		Outline:     &boardOutline{Source: "polygon", Format: "polyline", BBox: layoutBBox{MinX: 95, MinY: 195, MaxX: 605, MaxY: 905}, Points: [][2]float64{{100, 200}, {600, 200}, {600, 900}, {125, 900}, {100, 860}}},
		RoutedLines: &zero, Copper: &boardCopperSnapshot{Availability: map[string]string{"routing": "available"}, ArcsAvailable: &available, Lines: []any{}, Arcs: []any{}},
		Components: []boardComp{
			{ID: "top-part", Designator: "U1", Layer: 1, X: 175, Y: 300, Rotation: 37, BBox: &layoutBBox{MinX: 140, MinY: 270, MaxX: 220, MaxY: 360}, Pads: []boardPad{{ID: "rect", Number: "1", Layer: 1, X: 170, Y: 310, Rotation: 37, Shape: []any{"RECT", 40.0, 20.0, 3.0}}, {ID: "ellipse", Number: "2", Layer: 1, X: 240, Y: 470, Rotation: 20, Shape: []any{"ELLIPSE", 30.0, 50.0}}}},
			{ID: "bottom-part", Designator: "J1", Layer: 2, X: 450, Y: 660, BBox: &layoutBBox{MinX: 420, MinY: 630, MaxX: 490, MaxY: 690}, Pads: []boardPad{{ID: "through", Number: "1", Layer: 12, X: 330, Y: 450, Rotation: 90, Shape: []any{"OVAL", 70.0, 40.0}}, {ID: "bottom-pad", Number: "2", Layer: 2, X: 460, Y: 670, Shape: []any{"RECT", 20.0, 30.0, 0.0}}}},
		},
		Silk: []pcbSilkText{
			{ID: "top-text", Kind: "attribute", Key: "Designator", Text: "U1<&", Layer: 3, X: 162, Y: 345, Rotation: 37, FontSize: 22, BBox: &pcbRect{MinX: 155, MinY: 344, MaxX: 190, MaxY: 380}},
			{ID: "bottom-text", Kind: "attribute", Key: "Designator", Text: "J1", Layer: 4, X: 480, Y: 710, Rotation: 90, Mirror: true, FontSize: 25, BBox: &pcbRect{MinX: 455, MinY: 708, MaxX: 481, MaxY: 740}},
			{ID: "free-text", Kind: "string", Text: "TX", Layer: 3, X: 300, Y: 800, FontSize: 45, BBox: &pcbRect{MinX: 300, MinY: 750, MaxX: 358, MaxY: 800}},
		}}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	m["capture"] = map[string]any{"projectUuid": "synthetic-project", "documentUuid": "synthetic-pcb", "identityVerified": true}
	m["componentsAvailability"] = "available"
	m["availability"] = map[string]any{"silk": "available"}
	for _, v := range m["silk"].([]any) {
		v.(map[string]any)["lineWidth"] = 6.0
		v.(map[string]any)["lineWidthAvailability"] = "available"
		v.(map[string]any)["visible"] = true
		v.(map[string]any)["visibilityAvailability"] = "available"
		v.(map[string]any)["keyVisible"] = false
		v.(map[string]any)["keyVisibilityAvailability"] = "available"
	}
	for _, c := range m["components"].([]any) {
		for _, p := range c.(map[string]any)["pads"].([]any) {
			if _, ok := p.(map[string]any)["rotation"]; !ok {
				p.(map[string]any)["rotation"] = 0.0
			}
		}
	}
	raw, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func actualSVGElements(t *testing.T, raw []byte) ([]xml.StartElement, pcbActualMetadata) {
	t.Helper()
	var nodes []xml.StartElement
	var metadata pcbActualMetadata
	dec := xml.NewDecoder(bytes.NewReader(raw))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if start, ok := tok.(xml.StartElement); ok {
			nodes = append(nodes, start)
			if start.Name.Local == "metadata" {
				var text string
				if err := dec.DecodeElement(&text, &start); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(text), &metadata); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	return nodes, metadata
}

func actualXMLAttr(n xml.StartElement, key string) string {
	for _, a := range n.Attr {
		if a.Name.Local == key {
			return a.Value
		}
	}
	return ""
}
func actualNode(t *testing.T, nodes []xml.StartElement, key, value string) xml.StartElement {
	t.Helper()
	for _, n := range nodes {
		if actualXMLAttr(n, key) == value {
			return n
		}
	}
	t.Fatalf("missing node %s=%s", key, value)
	return xml.StartElement{}
}

func TestPCBActualRenderPreservesGeometryAndSource(t *testing.T) {
	raw := actualRenderFixture(t)
	before := append([]byte(nil), raw...)
	var svg bytes.Buffer
	if err := renderPCBActualSVG(&svg, raw, "both"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, before) {
		t.Fatal("renderer modified source bytes")
	}
	nodes, metadata := actualSVGElements(t, svg.Bytes())
	if metadata.Mode != "actual" || metadata.SourceSHA256 != sha256String(raw) || metadata.ProjectUUID != "synthetic-project" || metadata.DocumentUUID != "synthetic-pcb" || !metadata.IdentityVerified {
		t.Fatalf("unbound metadata: %+v", metadata)
	}
	if metadata.Side != "both" || metadata.DimensionsSource != "outline-points" || math.Abs(metadata.DimensionsMM.Width-12.7) > 1e-9 || math.Abs(metadata.DimensionsMM.Height-17.78) > 1e-9 {
		t.Fatalf("board dimensions used stroke envelope instead of centerline: %+v", metadata)
	}
	if metadata.RoutingStatus != "no-routed-primitives" {
		t.Fatalf("routing=%s", metadata.RoutingStatus)
	}
	if got := actualXMLAttr(actualNode(t, nodes, "data-board-transform", "top"), "transform"); got != "matrix(1 0 0 -1 -100.000000 900.000000)" {
		t.Fatalf("top transform=%s", got)
	}
	bottom := actualNode(t, nodes, "data-board-transform", "bottom")
	if actualXMLAttr(bottom, "transform") != "matrix(-1 0 0 -1 600.000000 900.000000)" || actualXMLAttr(bottom, "data-mirror-axis-x") != "350.000000" {
		t.Fatalf("bottom origin/reflection wrong: %+v", bottom)
	}
	for id, want := range map[string]string{"rect": "translate(170.000000 310.000000) rotate(37.000000)", "top-text": "translate(162.000000 345.000000) rotate(37.000000) scale(1 -1)", "bottom-text": "translate(480.000000 710.000000) rotate(90.000000) scale(-1 -1)"} {
		if got := actualXMLAttr(actualNode(t, nodes, "data-primitive-id", id), "transform"); got != want {
			t.Fatalf("%s moved or rotated incorrectly: %s", id, got)
		}
	}
	countTH := 0
	for _, n := range nodes {
		if actualXMLAttr(n, "data-primitive-id") == "through" {
			countTH++
			if actualXMLAttr(n, "data-through-hole") != "true" {
				t.Fatal("missing through-hole declaration")
			}
		}
	}
	if countTH != 2 {
		t.Fatalf("through-hole copper appears %d times, want both sides", countTH)
	}
	if !strings.Contains(svg.String(), "孔径及槽孔形状未采集") {
		t.Fatal("unavailable hole diameter was not disclosed")
	}
	if strings.Contains(svg.String(), `<circle`) {
		t.Fatal("renderer invented a drill circle")
	}
	if !strings.Contains(svg.String(), "U1&lt;&amp;") {
		t.Fatal("text was not XML escaped")
	}
}

func TestPCBActualRenderSidesAndKnownStyles(t *testing.T) {
	raw := actualRenderFixture(t)
	for _, side := range []string{"top", "bottom"} {
		t.Run(side, func(t *testing.T) {
			var out bytes.Buffer
			if err := renderPCBActualSVG(&out, raw, side); err != nil {
				t.Fatal(err)
			}
			nodes, m := actualSVGElements(t, out.Bytes())
			if m.Side != side {
				t.Fatalf("side=%s", m.Side)
			}
			wrong := "bottom-text"
			right := "top-text"
			if side == "bottom" {
				wrong, right = right, wrong
			}
			for _, n := range nodes {
				if actualXMLAttr(n, "data-primitive-id") == wrong {
					t.Fatalf("opposite side text %s leaked", wrong)
				}
			}
			if got := actualXMLAttr(actualNode(t, nodes, "data-primitive-id", right), "data-native-line-width"); got != "6.000000" {
				t.Fatalf("known width=%s", got)
			}
		})
	}
}

func TestPCBActualRenderMissingGeometryRemainsUnknown(t *testing.T) {
	var m map[string]any
	if err := json.Unmarshal(actualRenderFixture(t), &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "capture")
	delete(m, "availability")
	delete(m, "copper")
	delete(m, "routedLines")
	comp := m["components"].([]any)[0].(map[string]any)
	pad := comp["pads"].([]any)[0].(map[string]any)
	delete(pad, "shape")
	pad["width"], pad["height"] = 100, 100
	text := m["silk"].([]any)[0].(map[string]any)
	delete(text, "lineWidth")
	text["lineWidthAvailability"] = "unavailable"
	raw, _ := json.Marshal(m)
	var out bytes.Buffer
	if err := renderPCBActualSVG(&out, raw, "top"); err != nil {
		t.Fatal(err)
	}
	nodes, md := actualSVGElements(t, out.Bytes())
	if md.IdentityVerified || md.ProjectUUID != "" || md.RoutingStatus != "unknown" {
		t.Fatalf("unknown fields invented: %+v", md)
	}
	if actualXMLAttr(actualNode(t, nodes, "data-primitive-id", "top-text"), "data-native-line-width") != "unknown" {
		t.Fatal("invented default line width")
	}
	if !strings.Contains(out.String(), `data-geometry="unavailable"`) || !strings.Contains(out.String(), "红十字仅表示已读中心") {
		t.Fatal("unknown shape got nominal geometry")
	}
}

func TestPCBActualRenderRefusesCandidatesAndInvalidObservations(t *testing.T) {
	for _, tc := range []struct {
		name, key string
		value     any
	}{
		{"candidate", "baseSemanticHash", "hash"}, {"model", "stackup", map[string]any{}}, {"report", "candidates", []any{}}, {"missing capture time", "capturedAt", nil}, {"invalid time", "capturedAt", "yesterday"}, {"missing outline", "outline", nil}, {"missing inventory", "components", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var m map[string]any
			_ = json.Unmarshal(actualRenderFixture(t), &m)
			if tc.value == nil {
				delete(m, tc.key)
			} else {
				m[tc.key] = tc.value
			}
			raw, _ := json.Marshal(m)
			var out bytes.Buffer
			if err := renderPCBActualSVG(&out, raw, "top"); err == nil {
				t.Fatal("expected refusal")
			}
			if out.Len() != 0 {
				t.Fatal("wrote partial SVG on refusal")
			}
		})
	}
	var out bytes.Buffer
	if err := renderPCBActualSVG(&out, actualRenderFixture(t), "back"); err == nil {
		t.Fatal("accepted unsupported side")
	}
}

func TestPCBActualRenderCLISeparationAndNoSourceOverwrite(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "board.json")
	out := filepath.Join(dir, "actual.svg")
	raw := actualRenderFixture(t)
	if err := os.WriteFile(source, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	cmd := newPcbLayoutRenderCmd(&stdout, &stderr)
	cmd.SetArgs([]string{"--actual", "--board", source, "--side", "bottom", "--out", out})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	result, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	_, md := actualSVGElements(t, result)
	if md.Side != "bottom" {
		t.Fatal("CLI lost side")
	}
	for _, extra := range [][]string{{"--candidate", "unused.json"}, {"--from", "unused.json"}, {"--candidate-id", ""}, {"--side", "bad"}, {"--out", source}} {
		cmd := newPcbLayoutRenderCmd(&stdout, &stderr)
		args := []string{"--actual", "--board", source, "--out", out}
		cmd.SetArgs(append(args, extra...))
		if err := cmd.Execute(); err == nil {
			t.Fatalf("accepted incompatible args: %v", extra)
		}
	}
	cmd = newPcbLayoutRenderCmd(&stdout, &stderr)
	cmd.SetArgs([]string{"--board", source, "--candidate", "unused.json", "--out", out, "--side", "top"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("candidate accepted actual-only --side")
	}
	after, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, after) {
		t.Fatal("source overwritten")
	}
}

func TestPCBActualRenderArcSamplesBBoxAndRoutingLimits(t *testing.T) {
	var m map[string]any
	_ = json.Unmarshal(actualRenderFixture(t), &m)
	outline := m["outline"].(map[string]any)
	outline["format"] = "arc-polyline"
	copper := m["copper"].(map[string]any)
	copper["lines"] = []any{map[string]any{"primitiveId": "line-1"}}
	raw, _ := json.Marshal(m)
	var out bytes.Buffer
	if err := renderPCBActualSVG(&out, raw, "top"); err != nil {
		t.Fatal(err)
	}
	_, md := actualSVGElements(t, out.Bytes())
	if md.RoutingStatus != "routed-primitives-present" || !strings.Contains(out.String(), "板框圆弧按读取的采样点显示") {
		t.Fatal("lost arc/routing limits")
	}
	delete(outline, "points")
	outline["source"] = "bbox"
	raw, _ = json.Marshal(m)
	out.Reset()
	if err := renderPCBActualSVG(&out, raw, "top"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `data-kind="outline-bbox-only"`) || !strings.Contains(out.String(), "真实边界缺测") {
		t.Fatal("bbox fallback not marked")
	}
}

func TestPCBActualRenderVisibilityAndMissingPose(t *testing.T) {
	for _, tc := range []struct {
		name     string
		edit     func(map[string]any)
		hiddenID string
		note     string
	}{
		{"hidden text", func(m map[string]any) { m["silk"].([]any)[0].(map[string]any)["visible"] = false }, "top-text", ""},
		{"unknown visibility", func(m map[string]any) { delete(m["silk"].([]any)[0].(map[string]any), "visible") }, "top-text", "文字可见性缺测"},
		{"failed visibility", func(m map[string]any) {
			m["silk"].([]any)[0].(map[string]any)["visibilityAvailability"] = "unavailable"
		}, "top-text", "文字可见性缺测"},
		{"missing pad x", func(m map[string]any) {
			delete(m["components"].([]any)[0].(map[string]any)["pads"].([]any)[0].(map[string]any), "x")
		}, "rect", "焊盘位置缺测"},
		{"missing text mirror", func(m map[string]any) { delete(m["silk"].([]any)[0].(map[string]any), "Mirror") }, "top-text", "文字位置／字号缺测"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var m map[string]any
			_ = json.Unmarshal(actualRenderFixture(t), &m)
			tc.edit(m)
			raw, _ := json.Marshal(m)
			var out bytes.Buffer
			if err := renderPCBActualSVG(&out, raw, "top"); err != nil {
				t.Fatal(err)
			}
			nodes, _ := actualSVGElements(t, out.Bytes())
			for _, n := range nodes {
				if actualXMLAttr(n, "data-primitive-id") == tc.hiddenID {
					t.Fatalf("unknown/hidden primitive rendered: %s", tc.hiddenID)
				}
			}
			if tc.note != "" && !strings.Contains(out.String(), tc.note) {
				t.Fatalf("missing limitation %s", tc.note)
			}
		})
	}
}

func TestPCBActualRenderMissingRoutingArrays(t *testing.T) {
	for _, field := range []string{"lines", "arcs"} {
		t.Run(field, func(t *testing.T) {
			var m map[string]any
			_ = json.Unmarshal(actualRenderFixture(t), &m)
			m["copper"].(map[string]any)[field] = nil
			delete(m, "routedLines")
			raw, _ := json.Marshal(m)
			var out bytes.Buffer
			if err := renderPCBActualSVG(&out, raw, "top"); err != nil {
				t.Fatal(err)
			}
			_, metadata := actualSVGElements(t, out.Bytes())
			if metadata.RoutingStatus != "unknown" {
				t.Fatalf("missing %s became empty routing: %s", field, metadata.RoutingStatus)
			}
		})
	}
}

func TestPCBActualRenderSummarizesCaptureFailures(t *testing.T) {
	var firstViewBox string
	var firstFooterCount int
	for _, count := range []int{1, 450} {
		var source map[string]any
		_ = json.Unmarshal(actualRenderFixture(t), &source)
		partial := make([]string, count)
		for i := range partial {
			partial[i] = fmt.Sprintf("silk object-%03d visibility and line width unavailable: %s", i, strings.Repeat("detail ", 80))
		}
		source["partial"] = partial
		raw, _ := json.Marshal(source)
		var out bytes.Buffer
		if err := renderPCBActualSVG(&out, raw, "both"); err != nil {
			t.Fatal(err)
		}
		nodes, metadata := actualSVGElements(t, out.Bytes())
		missing := 0
		for _, note := range metadata.Limitations {
			if strings.HasPrefix(note, "采集缺项：") {
				missing++
			}
		}
		if missing != count || !strings.Contains(out.String(), fmt.Sprintf("另有 %d 项采集缺项", count)) {
			t.Fatalf("capture details/count lost: got %d, want %d", missing, count)
		}
		footerCount := strings.Count(out.String(), `<text class="limitation"`)
		viewBox := actualXMLAttr(nodes[0], "viewBox")
		if count == 1 {
			firstViewBox, firstFooterCount = viewBox, footerCount
		} else if viewBox != firstViewBox || footerCount != firstFooterCount {
			t.Fatalf("per-object errors inflated the page: viewBox %s vs %s, footer %d vs %d", viewBox, firstViewBox, footerCount, firstFooterCount)
		}
		if got := actualXMLAttr(actualNode(t, nodes, "data-primitive-id", "top-text"), "transform"); got != "translate(162.000000 345.000000) rotate(37.000000) scale(1 -1)" {
			t.Fatalf("design text moved during summary: %s", got)
		}
	}
}

func TestPCBActualRenderBoundsRepeatedVisibilityWarnings(t *testing.T) {
	var source map[string]any
	if err := json.Unmarshal(actualRenderFixture(t), &source); err != nil {
		t.Fatal(err)
	}
	texts := []any{}
	for i := 0; i < 100; i++ {
		texts = append(texts, map[string]any{"id": fmt.Sprintf("missing-%d", i), "kind": "attribute", "layer": 3, "text": "unreadable visibility"})
	}
	source["silk"] = texts
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := renderPCBActualSVG(&out, raw, "top"); err != nil {
		t.Fatal(err)
	}
	_, meta := actualSVGElements(t, out.Bytes())
	seen := map[string]bool{}
	for _, note := range meta.Limitations {
		if seen[note] {
			t.Fatalf("repeated limit inflated drawing: %s", note)
		}
		seen[note] = true
	}
	if strings.Count(out.String(), `class="limitation"`) > 20 {
		t.Fatal("repeated unknown visibility inflated page")
	}
}

func TestPCBActualRenderFreeStringHangsBelowAnchor(t *testing.T) {
	var svg bytes.Buffer
	if err := renderPCBActualSVG(&svg, actualRenderFixture(t), "top"); err != nil {
		t.Fatal(err)
	}
	out := svg.String()
	i := strings.Index(out, `data-primitive-id="free-text"`)
	if i < 0 {
		t.Fatal("free string not rendered")
	}
	if seg := out[i:]; !strings.Contains(seg[:strings.Index(seg, "</g>")], `dominant-baseline="text-before-edge"`) {
		t.Fatalf("free string drawn above its top-left anchor: %s", seg[:200])
	}
	j := strings.Index(out, `data-primitive-id="top-text"`)
	if seg := out[j:]; !strings.Contains(seg[:strings.Index(seg, "</g>")], `dominant-baseline="text-after-edge"`) {
		t.Fatal("designator baseline changed")
	}
}
