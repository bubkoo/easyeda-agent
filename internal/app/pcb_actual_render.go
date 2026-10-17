package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"math"
	"sort"
	"strings"
	"time"
)

// Actual drawings consume the saved bytes directly. In particular they never
// pass through the solve adapter, which projects candidates and may reject
// partial observations that remain useful in a clearly qualified drawing.
type pcbActualMetadata struct {
	SchemaVersion    int                 `json:"schemaVersion"`
	Mode             string              `json:"mode"`
	SourceSHA256     string              `json:"sourceSha256"`
	ProjectUUID      string              `json:"projectUuid,omitempty"`
	DocumentUUID     string              `json:"documentUuid,omitempty"`
	CapturedAt       string              `json:"capturedAt"`
	IdentityVerified bool                `json:"identityVerified"`
	Side             string              `json:"side"`
	Orientation      string              `json:"orientation"`
	DimensionsMM     pcbActualDimensions `json:"dimensionsMM"`
	DimensionsSource string              `json:"dimensionsSource"`
	RoutingStatus    string              `json:"routingStatus"`
	Limitations      []string            `json:"limitations"`
}

type pcbActualDimensions struct {
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type pcbActualDrawing struct {
	Snapshot     pcbActualSnapshot
	Metadata     pcbActualMetadata
	Bounds       layoutBBox
	RoutingLabel string
}

// Private observation projections keep the renderer independent from solver
// coverage-model changes. Optional raw fields are preserved even when decoding
// an older boardSnapshot schema, and absent poses never become real zeroes.
type pcbActualSnapshot struct {
	boardSnapshot
	Components             []pcbActualComponent `json:"components"`
	ComponentsAvailability string               `json:"componentsAvailability,omitempty"`
	Silk                   []pcbActualText      `json:"silk,omitempty"`
}

type pcbActualComponent struct {
	boardComp
	Pads         []pcbActualPad    `json:"pads"`
	Availability map[string]string `json:"availability,omitempty"`
	PadFailures  []map[string]any  `json:"padFailures,omitempty"`
	anchorKnown  bool
}

type pcbActualPad struct {
	boardPad
	Availability   map[string]string `json:"availability,omitempty"`
	CaptureErrors  map[string]any    `json:"captureErrors,omitempty"`
	GeometryStatus string            `json:"geometryStatus,omitempty"`
	GeometryReason string            `json:"geometryReason,omitempty"`
	poseKnown      bool
}

type pcbActualText struct {
	pcbSilkText
	LineWidth                 *float64 `json:"lineWidth,omitempty"`
	LineWidthAvailability     string   `json:"lineWidthAvailability,omitempty"`
	FontFamily                string   `json:"FontFamily,omitempty"`
	Visible                   *bool    `json:"visible,omitempty"`
	VisibilityAvailability    string   `json:"visibilityAvailability,omitempty"`
	KeyVisible                *bool    `json:"keyVisible,omitempty"`
	KeyVisibilityAvailability string   `json:"keyVisibilityAvailability,omitempty"`
	poseKnown                 bool
}

func (c *pcbActualComponent) UnmarshalJSON(raw []byte) error {
	type plain pcbActualComponent
	var value plain
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	*c = pcbActualComponent(value)
	c.anchorKnown = actualFieldsPresent(raw, "x", "y", "rotation")
	return nil
}

func (p *pcbActualPad) UnmarshalJSON(raw []byte) error {
	type plain pcbActualPad
	var value plain
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	*p = pcbActualPad(value)
	p.poseKnown = actualFieldsPresent(raw, "x", "y", "rotation", "layer")
	return nil
}

func (t *pcbActualText) UnmarshalJSON(raw []byte) error {
	type plain pcbActualText
	var value plain
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	*t = pcbActualText(value)
	t.poseKnown = actualFieldsPresent(raw, "x", "y", "rotation", "fontsize", "mirror", "reverse")
	return nil
}

func actualFieldsPresent(raw []byte, keys ...string) bool {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return false
	}
	lower := make(map[string]json.RawMessage, len(fields))
	for key, value := range fields {
		lower[strings.ToLower(key)] = value
	}
	for _, key := range keys {
		value, ok := lower[key]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return false
		}
	}
	return true
}

func preparePCBActualDrawing(raw []byte, side string) (*pcbActualDrawing, error) {
	if side != "top" && side != "bottom" && side != "both" {
		return nil, fmt.Errorf("--side must be top, bottom or both")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("decode actual PCB readback: %w", err)
	}
	// Reject solve/model/report envelopes instead of treating their zero-value
	// fields as an empty readback. Capture time is a required observation marker.
	for _, key := range []string{"candidate", "candidates", "baseSemanticHash", "moveReasons", "stackup", "board"} {
		if _, exists := fields[key]; exists {
			return nil, fmt.Errorf("actual render requires a pcb dump readback, not a candidate/model/report (%s)", key)
		}
	}
	if _, exists := fields["components"]; !exists {
		return nil, fmt.Errorf("actual PCB readback has no components inventory")
	}
	var d pcbActualDrawing
	if err := json.Unmarshal(raw, &d.Snapshot); err != nil {
		return nil, fmt.Errorf("decode actual PCB readback: %w", err)
	}
	s := &d.Snapshot
	if _, err := time.Parse(time.RFC3339Nano, s.CapturedAt); err != nil {
		return nil, fmt.Errorf("actual PCB readback requires a valid capturedAt timestamp")
	}
	if s.Outline == nil || !actualValidRect(s.Outline.BBox) {
		return nil, fmt.Errorf("actual PCB readback has no valid observed outline bounds")
	}
	d.Bounds = s.Outline.BBox
	d.Metadata = pcbActualMetadata{SchemaVersion: 1, Mode: "actual", SourceSHA256: sha256String(raw),
		CapturedAt: s.CapturedAt, Side: side, DimensionsSource: "outline-bbox",
		Limitations: []string{
			"辅助装配图，非官方 EDA 截图；不代表 DRC、连接、装配或制造验收通过。",
			"器件矩形为读取到的包络，可能包含封装丝印；不是实体外形或 IPC courtyard。",
			"仅显示板框、器件包络、焊盘铜形与丝印文字；不显示完整走线、铺铜、阻焊或封装图形。",
		}}
	// This small envelope decoder intentionally shares the JSON contract rather
	// than deriving identity from a project label, file name or adjacent artifact.
	var envelope struct {
		Capture struct {
			ProjectUUID      string `json:"projectUuid"`
			DocumentUUID     string `json:"documentUuid"`
			IdentityVerified bool   `json:"identityVerified"`
		} `json:"capture"`
		Availability map[string]string `json:"availability"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	d.Metadata.ProjectUUID = envelope.Capture.ProjectUUID
	d.Metadata.DocumentUUID = envelope.Capture.DocumentUUID
	d.Metadata.IdentityVerified = envelope.Capture.IdentityVerified
	if !envelope.Capture.IdentityVerified || envelope.Capture.ProjectUUID == "" || envelope.Capture.DocumentUUID == "" {
		d.note("快照缺少完整且已核对的工程／文档身份；此图不补证现场身份。")
	}
	if s.Components == nil || s.ComponentsAvailability != "" && s.ComponentsAvailability != "available" {
		d.note("器件清单读取不完整；空清单不代表空板。")
	}
	if state, ok := envelope.Availability["silk"]; !ok || state != "available" {
		if len(s.Silk) == 0 {
			d.note("丝印清单缺测或未声明完整性，无法证明板上没有文字。")
		} else {
			d.note("丝印清单未声明完整性；仅显示已读取的文字。")
		}
	}
	if len(s.Outline.Points) >= 3 && s.Outline.Source == "polygon" {
		b := layoutBBox{MinX: math.Inf(1), MinY: math.Inf(1), MaxX: math.Inf(-1), MaxY: math.Inf(-1)}
		for _, p := range s.Outline.Points {
			if !allFinite(p[0], p[1]) {
				return nil, fmt.Errorf("actual outline contains a non-finite point")
			}
			b.MinX, b.MinY = math.Min(b.MinX, p[0]), math.Min(b.MinY, p[1])
			b.MaxX, b.MaxY = math.Max(b.MaxX, p[0]), math.Max(b.MaxY, p[1])
		}
		if !actualValidRect(b) {
			return nil, fmt.Errorf("actual outline polygon is degenerate")
		}
		d.Bounds = b // Polygon centerline excludes the display stroke's envelope.
		d.Metadata.DimensionsSource = "outline-points"
		if strings.Contains(s.Outline.Format, "arc") {
			d.note("板框圆弧按读取的采样点显示；不是恢复出的原生圆弧参数。")
		}
	} else {
		d.note("板框只有包围盒，真实边界缺测；图中使用虚线包围盒。")
	}
	d.Metadata.DimensionsMM = pcbActualDimensions{Width: (d.Bounds.MaxX - d.Bounds.MinX) * .0254, Height: (d.Bounds.MaxY - d.Bounds.MinY) * .0254}
	switch side {
	case "top":
		d.Metadata.Orientation = "top-unmirrored"
	case "bottom":
		d.Metadata.Orientation = "bottom-mirrored-about-board-center"
	default:
		d.Metadata.Orientation = "top-and-bottom"
	}
	d.Metadata.RoutingStatus, d.RoutingLabel = actualRoutingStatus(s)
	for _, limitation := range s.Partial {
		d.note("采集缺项：" + limitation)
	}
	return &d, nil
}

func (d *pcbActualDrawing) note(note string) {
	d.Metadata.Limitations = append(d.Metadata.Limitations, note)
}
func actualValidRect(b layoutBBox) bool {
	return allFinite(b.MinX, b.MinY, b.MaxX, b.MaxY) && b.MaxX > b.MinX && b.MaxY > b.MinY
}

func actualRoutingStatus(s *pcbActualSnapshot) (string, string) {
	if c := s.Copper; c != nil && c.Availability["routing"] == "available" && c.ArcsAvailable != nil && *c.ArcsAvailable && c.Lines != nil && c.Arcs != nil {
		n := len(c.Lines) + len(c.Arcs)
		if n == 0 {
			return "no-routed-primitives", "未记录已布线段／圆弧；网络连通状态待独立核对"
		}
		return "routed-primitives-present", fmt.Sprintf("已读取 %d 条走线／圆弧；此装配图不显示布线，也不证明布线完成", n)
	}
	if s.RoutedLines != nil {
		return "lines-only", fmt.Sprintf("已读取 %d 条直线；圆弧及完整布线状态未核对", *s.RoutedLines)
	}
	return "unknown", "布线状态缺测"
}

func renderPCBActualSVG(w io.Writer, raw []byte, side string) error {
	d, err := preparePCBActualDrawing(raw, side)
	if err != nil {
		return err
	}
	b := d.Bounds
	boardW, boardH := b.MaxX-b.MinX, b.MaxY-b.MinY
	panelW := math.Max(600, boardW+100)
	sides := []string{side}
	if side == "both" {
		sides = []string{"top", "bottom"}
	}
	width := panelW * float64(len(sides))
	headerH := 170.0
	var panels bytes.Buffer
	for i, oneSide := range sides {
		offset := float64(i)*panelW + (panelW-boardW)/2
		fmt.Fprintf(&panels, `<g data-side="%s"><text class="side-title" x="%.6f" y="145">%s</text><g transform="translate(%.6f %.6f)">`, oneSide, float64(i)*panelW+24, actualSideTitle(oneSide), offset, headerH)
		if oneSide == "top" {
			fmt.Fprintf(&panels, `<g data-board-transform="top" transform="matrix(1 0 0 -1 %.6f %.6f)">`, -b.MinX, b.MaxY)
		} else {
			fmt.Fprintf(&panels, `<g data-board-transform="bottom" data-mirror-axis-x="%.6f" transform="matrix(-1 0 0 -1 %.6f %.6f)">`, (b.MinX+b.MaxX)/2, b.MaxX, b.MaxY)
		}
		d.renderActualBoard(&panels, oneSide)
		fmt.Fprint(&panels, `</g></g></g>`)
	}
	sort.Strings(d.Metadata.Limitations)
	d.Metadata.Limitations = uniqueStrings(d.Metadata.Limitations)
	var footer []string
	for _, note := range actualFooterNotes(d.Metadata.Limitations) {
		footer = append(footer, actualWrap(note, max(24, int((width-48)/10)))...)
	}
	height := headerH + boardH + 50 + float64(len(footer))*23
	metadata, err := json.Marshal(d.Metadata)
	if err != nil {
		return err
	}
	var out bytes.Buffer
	fmt.Fprintf(&out, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %.6f %.6f" width="1200" height="%.0f" role="img"><title>PCB 实际回读辅助装配图</title><metadata id="easyeda-actual-readback">%s</metadata>`, width, height, height/width*1200, html.EscapeString(string(metadata)))
	fmt.Fprint(&out, `<style>.heading{font:bold 25px sans-serif;fill:#102d38}.caption{font:16px sans-serif;fill:#405561}.side-title{font:bold 19px sans-serif;fill:#102d38}.limitation{font:15px sans-serif;fill:#695025}.component-envelope{fill:#c6dadd;fill-opacity:.24;stroke:#537782;stroke-width:1;stroke-dasharray:6 4}.pad{fill:#dca64a;stroke:#8b6628;stroke-width:.6}.silk{fill:#183a41}.unknown{stroke:#b73d3d;stroke-width:2;fill:none}.text-envelope{fill:none;stroke:#739395;stroke-width:.6;stroke-dasharray:2 2;opacity:.45}</style>`)
	fmt.Fprintf(&out, `<rect width="%.6f" height="%.6f" fill="#f7fafb"/><text class="heading" x="24" y="34">PCB 实际回读 · 辅助装配图</text><text class="caption" x="24" y="62">%.3f × %.3f mm · %d 个器件 · %s</text><text class="caption" x="24" y="87">%s</text><text class="caption" x="24" y="111">源 SHA256 %s</text>`, width, height, d.Metadata.DimensionsMM.Width, d.Metadata.DimensionsMM.Height, len(d.Snapshot.Components), html.EscapeString(d.Metadata.CapturedAt), html.EscapeString(d.RoutingLabel), d.Metadata.SourceSHA256)
	out.Write(panels.Bytes())
	for i, line := range footer {
		fmt.Fprintf(&out, `<text class="limitation" x="24" y="%.6f">%s</text>`, headerH+boardH+40+float64(i)*23, html.EscapeString(line))
	}
	fmt.Fprint(&out, `</svg>`)
	_, err = w.Write(out.Bytes())
	return err
}

// Per-object capture errors remain fully preserved in metadata, but hundreds
// of them must not shrink a board into an unreadable sliver in the drawing.
// Only the evidence list is summarized; design text and structural limitations
// keep their existing positions/content.
func actualFooterNotes(limitations []string) []string {
	notes := make([]string, 0, len(limitations))
	missing := 0
	for _, note := range limitations {
		if strings.HasPrefix(note, "采集缺项：") {
			missing++
			continue
		}
		notes = append(notes, note)
	}
	if missing > 0 {
		notes = append(notes, fmt.Sprintf("另有 %d 项采集缺项；逐项详情见 SVG metadata 与原始回读。", missing))
	}
	return notes
}

func actualSideTitle(side string) string {
	if side == "bottom" {
		return "BOTTOM · 镜像装配底视"
	}
	return "TOP · 顶视"
}

func actualWrap(s string, n int) []string {
	var lines []string
	runes := []rune(s)
	for len(runes) > n {
		lines = append(lines, string(runes[:n]))
		runes = runes[n:]
	}
	if len(runes) > 0 {
		lines = append(lines, string(runes))
	}
	return lines
}

func (d *pcbActualDrawing) renderActualBoard(w io.Writer, side string) {
	s, b := &d.Snapshot, d.Bounds
	layer, silkLayer := pcbSideTop, silkTopLayer
	if side == "bottom" {
		layer, silkLayer = pcbSideBottom, silkBottomLayer
	}
	if s.Outline.Source == "polygon" && len(s.Outline.Points) >= 3 {
		var points []string
		for _, p := range s.Outline.Points {
			points = append(points, fmt.Sprintf("%.6f,%.6f", p[0], p[1]))
		}
		fmt.Fprintf(w, `<polygon data-kind="outline" points="%s" fill="#fff" stroke="#173e48" stroke-width="2"/>`, strings.Join(points, " "))
	} else {
		fmt.Fprintf(w, `<rect data-kind="outline-bbox-only" x="%.6f" y="%.6f" width="%.6f" height="%.6f" fill="#fff" stroke="#9a6929" stroke-width="2" stroke-dasharray="8 5"/>`, b.MinX, b.MinY, b.MaxX-b.MinX, b.MaxY-b.MinY)
	}
	for _, c := range s.Components {
		if c.Layer != pcbSideTop && c.Layer != pcbSideBottom {
			d.note("存在装配面未知的器件；未猜测其显示面。")
		}
		if c.Layer != layer {
			continue
		}
		if c.BBox == nil || !actualValidRect(*c.BBox) {
			d.note("部分器件包络缺测；未生成替代外形。")
			continue
		}
		bb := c.BBox
		fmt.Fprintf(w, `<g data-kind="component" data-primitive-id="%s" data-ref="%s"`, html.EscapeString(c.ID), html.EscapeString(c.Designator))
		if c.anchorKnown && allFinite(c.X, c.Y, c.Rotation) {
			fmt.Fprintf(w, ` data-anchor-x="%.6f" data-anchor-y="%.6f" data-rotation="%.6f"`, c.X, c.Y, c.Rotation)
		} else {
			d.note("部分器件锚点缺测；仅显示已读包络，未用其中心代替锚点。")
			fmt.Fprint(w, ` data-anchor-status="unknown"`)
		}
		fmt.Fprintf(w, `><title>%s · 读取包络，非实体外形</title><rect class="component-envelope" x="%.6f" y="%.6f" width="%.6f" height="%.6f"/></g>`, html.EscapeString(c.Designator), bb.MinX, bb.MinY, bb.MaxX-bb.MinX, bb.MaxY-bb.MinY)
	}
	for _, c := range s.Components {
		if c.Pads == nil || len(c.PadFailures) > 0 || c.Availability["pads"] != "" && c.Availability["pads"] != "available" {
			d.note("部分器件焊盘清单缺测；未把缺测当作没有焊盘。")
		}
		for _, p := range c.Pads {
			if p.Layer == layer || p.Layer == pcbLayerMulti {
				d.renderActualPad(w, c, p)
			}
		}
	}
	for _, t := range s.Silk {
		if t.Layer == silkLayer {
			d.renderActualText(w, t)
		}
	}
}

func (d *pcbActualDrawing) renderActualPad(w io.Writer, c pcbActualComponent, p pcbActualPad) {
	if !p.poseKnown || !allFinite(p.X, p.Y, p.Rotation) || p.GeometryStatus == "unknown" && strings.Contains(p.GeometryReason, "pose") {
		d.note("部分焊盘位置缺测；未使用零坐标替代。")
		return
	}
	measured := pcbPadP{X: p.X, Y: p.Y, Rotation: p.Rotation}
	err := parseNetPathPadShape(map[string]any{"shape": p.Shape, "specialPad": p.SpecialPad, "availability": p.Availability, "captureErrors": p.CaptureErrors}, &measured, c.Designator+"."+p.Number)
	// Keep this check local as older shape parsers predate capture availability.
	for _, field := range []string{"shape", "specialPad"} {
		if state, exists := p.Availability[field]; exists && state != "available" {
			measured.ShapeOK = false
		}
		if p.CaptureErrors[field] != nil {
			measured.ShapeOK = false
		}
	}
	if p.GeometryStatus == "unknown" && p.GeometryReason != "" {
		measured.ShapeOK = false
	}
	if p.Layer == pcbLayerMulti {
		d.note("多层焊盘仅显示已读铜形；孔径及槽孔形状未采集，未绘制推测孔。")
	}
	fmt.Fprintf(w, `<g data-kind="pad" data-primitive-id="%s" data-ref="%s" data-pad-number="%s" data-layer="%d" data-through-hole="%t" data-x="%.6f" data-y="%.6f" data-rotation="%.6f" transform="translate(%.6f %.6f) rotate(%.6f)"><title>%s.%s · %s</title>`, html.EscapeString(p.ID), html.EscapeString(c.Designator), html.EscapeString(p.Number), p.Layer, p.Layer == pcbLayerMulti, p.X, p.Y, p.Rotation, p.X, p.Y, p.Rotation, html.EscapeString(c.Designator), html.EscapeString(p.Number), html.EscapeString(p.Net))
	if err != nil || !measured.ShapeOK {
		d.note("部分焊盘铜形缺测或不支持；红十字仅表示已读中心，不表示铜尺寸。")
		fmt.Fprint(w, `<path class="unknown" data-geometry="unavailable" d="M-4,-4 L4,4 M-4,4 L4,-4"/>`)
	} else {
		pw, ph := measured.ShapeW, measured.ShapeH
		switch measured.Shape {
		case "RECT":
			fmt.Fprintf(w, `<rect class="pad" x="%.6f" y="%.6f" width="%.6f" height="%.6f" rx="%.6f"/>`, -pw/2, -ph/2, pw, ph, measured.ShapeRound)
		case "ELLIPSE":
			fmt.Fprintf(w, `<ellipse class="pad" cx="0" cy="0" rx="%.6f" ry="%.6f"/>`, pw/2, ph/2)
		case "OVAL":
			fmt.Fprintf(w, `<rect class="pad" data-shape="oval" x="%.6f" y="%.6f" width="%.6f" height="%.6f" rx="%.6f"/>`, -pw/2, -ph/2, pw, ph, math.Min(pw, ph)/2)
		case "NGON":
			var points []string
			for i := 0; i < measured.ShapeSides; i++ {
				a := float64(i) * 2 * math.Pi / float64(measured.ShapeSides)
				points = append(points, fmt.Sprintf("%.6f,%.6f", pw/2*math.Cos(a), pw/2*math.Sin(a)))
			}
			fmt.Fprintf(w, `<polygon class="pad" points="%s"/>`, strings.Join(points, " "))
		}
	}
	fmt.Fprint(w, `</g>`)
}

func (d *pcbActualDrawing) renderActualText(w io.Writer, t pcbActualText) {
	if t.Kind == "attribute" {
		if t.KeyVisible != nil && t.KeyVisibilityAvailability == "available" {
			if *t.KeyVisible {
				d.note("存在可见属性键名；其原生键值组合排版未采集，图中未猜测键名位置。")
			}
		} else {
			d.note("部分属性键名显隐缺测；未猜测键名是否显示及其位置。")
		}
	}
	if t.Visible == nil || t.VisibilityAvailability != "available" {
		d.note("部分文字可见性缺测；未默认显示隐藏属性，需重新采集以显示这些文字。")
		return
	}
	if !*t.Visible {
		return
	}
	if !t.poseKnown || !allFinite(t.X, t.Y, t.Rotation, t.FontSize) || t.FontSize <= 0 {
		d.note("部分文字位置／字号缺测；未用默认字号或零坐标替代。")
		return
	}
	// Native stroke-font glyph outlines are not present in pcb dump. Preserve
	// exact placement/style in metadata, show an SVG-font reading aid and the
	// observed envelope without fitting/recentering/reflowing the label.
	d.note("文字保持已读锚点、旋转和镜像；浏览器字形仅为阅读辅助，线宽保留为数据，不能代替原生丝印轮廓。")
	if t.BBox != nil && actualValidRect(layoutBBox{MinX: t.BBox.MinX, MinY: t.BBox.MinY, MaxX: t.BBox.MaxX, MaxY: t.BBox.MaxY}) {
		fmt.Fprintf(w, `<rect class="text-envelope" data-text-bbox="%s" x="%.6f" y="%.6f" width="%.6f" height="%.6f"/>`, html.EscapeString(t.ID), t.BBox.MinX, t.BBox.MinY, t.BBox.MaxX-t.BBox.MinX, t.BBox.MaxY-t.BBox.MinY)
	} else {
		d.note("部分文字没有原生包络；图中文字范围随浏览器字体变化。")
	}
	lineWidth := "unknown"
	if t.LineWidth != nil && allFinite(*t.LineWidth) && *t.LineWidth > 0 && t.LineWidthAvailability == "available" {
		lineWidth = fmt.Sprintf("%.6f", *t.LineWidth)
	} else {
		d.note("部分丝印线宽未知；未用默认值代替。")
	}
	family := t.FontFamily
	if family == "" {
		family = "sans-serif"
	}
	sx := 1.0
	if t.Mirror {
		sx = -1
	}
	fmt.Fprintf(w, `<g data-kind="silk" data-primitive-id="%s" data-x="%.6f" data-y="%.6f" data-mirror="%t" data-reverse="%t" data-rotation="%.6f" data-native-line-width="%s" transform="translate(%.6f %.6f) rotate(%.6f) scale(%.0f -1)"><title>%s</title>`, html.EscapeString(t.ID), t.X, t.Y, t.Mirror, t.Reverse, t.Rotation, lineWidth, t.X, t.Y, t.Rotation, sx, html.EscapeString(t.Text))
	if t.Reverse {
		d.note("存在 reverse 文字；其原生字形语义未核验，仅显示锚点和读取包络。")
		fmt.Fprint(w, `<path class="unknown" d="M-4,-4 L4,4 M-4,4 L4,-4"/>`)
	} else {
		// A component attribute anchors bottom-left (text above the anchor); a free
		// string anchors top-left (text hangs below it) — measured on Web 4.1.78.
		baseline := "text-after-edge"
		if t.Kind == "string" {
			baseline = "text-before-edge"
		}
		fmt.Fprintf(w, `<text class="silk" x="0" y="0" dominant-baseline="%s" font-size="%.6f" font-family="%s" xml:space="preserve">%s</text>`, baseline, t.FontSize, html.EscapeString(family), html.EscapeString(t.Text))
	}
	fmt.Fprint(w, `</g>`)
}
