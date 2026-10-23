# PCB 参数化外壳：实现细节

主文档见 [PCB 参数化外壳设计](pcb-enclosure-design.md)。

## 坐标

- 单位 mm，XY 与 PCB 相同（y 向上），由回读的 mil 乘 0.0254 换算。
- `z = 0` 是下壳外底面。板底 `zBoardBottom = floor + standoff`，板面 `zBoardTop = zBoardBottom + board.thickness`。
- 外壳总高 `H = zBoardTop + maxTopHeight + topClearance + lid`；`split` 默认等于 `zBoardTop`。

## 参数文件 `spec.json`（version 1）

```json
{
  "version": 1, "unit": "mm", "resolution": 0.1, "type": "split|slide",
  "slide": {"side": "x+", "groove": 1, "gap": 0.2, "retain": 1},
  "source": {"boardSha256": "...", "project": "ceshi", "documentUuid": "..."},
  "outline": {"mode": "board|rect", "clearance": 0.5},
  "wall": 2, "floor": 2, "lid": 2,
  "board": {"outline": [[x,y],...], "thickness": 1.6, "standoff": 3},
  "height": 20, "split": 6.6,
  "lip": {"height": 2, "gap": 0.2, "snapDepth": 0.3, "snaps": [{"id": "snap-x-", "x": -16.7, "y": 1.1, "w": 6}]},
  "pillars": [{"id": "H1", "x": 0, "y": 0, "screw": "M2", "source": "component:H1"}],
  "features": [{"id": "J1-side", "op": "cut", "part": "both", "shape": "rect",
                "x": 0, "y": 0, "w": 10, "h": 4, "rotation": 0, "z": [6.6, 10], "source": "edge-connector:J1"}],
  "components": [{"ref": "J1", "side": "top", "bbox": [x0,y0,x1,y1], "height": 8.5, "heightSource": "model-name|attribute|default|spec"}]
}
```

- `board.outline` 是板框多边形（mm）。外形轮廓在 `board` 模式下就是板框，`rect` 模式下是板框包围盒。
  腔体是轮廓内部及向外 `clearance` 以内的区域，壁是再向外 `wall` 的环带；按距离外扩，外角自然成圆角
  （半径 = `clearance + wall`）。
- `features[].shape`：`rect` 用 `w,h,rotation`（中心 `x,y`），`circle` 用 `d`。`part`：`top|bottom|both`。
  同一位置先加后减：所有 `add` 之后再执行 `cut`，螺丝孔属于 `cut`。
- `components` 由 `plan` 写入，`build` 用来做干涉检查；手改高度时 `heightSource` 写 `spec`。

## 螺丝表（mm）

| 规格 | 柱外径 | 上壳导孔 | 下壳过孔 | 沉头直径/深度 |
|---|---|---|---|---|
| M2 | 5 | 1.6 | 2.4 | 4.0 / 2.0 |
| M2.5 | 6 | 2.1 | 2.9 | 5.0 / 2.5 |
| M3 | 7 | 2.5 | 3.4 | 6.0 / 3.0 |
| M4 | 9 | 3.3 | 4.5 | 8.0 / 4.0 |
| M5 | 11 | 4.2 | 5.5 | 9.5 / 5.0 |
| M6 | 13 | 5.0 | 6.6 | 11.0 / 6.0 |

展开：下壳柱 `[floor, zBoardBottom]` 外径实体，过孔贯穿 `[0, zBoardBottom]`，底面沉头；
上壳柱 `[zBoardTop, H - lid]` 外径实体，导孔 `[zBoardTop, H - lid]`（盲孔，不打穿顶板）。
沉头深度须小于板底高度（螺丝头可压在柱体上）。

## 自动推导（`plan`）

- 器件高度优先级：`--height REF=mm`（标 `spec`）→ 模型名 `-H<数字>` → `Height` 属性 →
  `--default-height`（标 `default`，并列入 warnings）。
- 侧面开口：位号前缀 `J|CN|USB|P|CON` 且包围盒距板框包围盒边 ≤ `--edge-margin`（默认 1 mm）
  或越出板框。开口贴该边、穿透壁厚，宽度为器件沿边尺寸加 `--cutout-margin`（默认 0.5 mm），
  Z 为 `[zBoardTop, zBoardTop + 器件高度 + margin]`，`part: both`。只处理与坐标轴平行的边。
- 螺丝柱：位号 `H|MH` 前缀或封装名含 `HOLE|MOUNT`，取器件锚点；规格由封装名 `M2..M6` 识别，缺省 `--screw`。
- 顶面开孔：只对 `--top-cutout` 点名的器件，按其包围盒加余量开通孔（顶板全厚）；
  没点名的按键/LED 写入 `candidates` 供参考。
- 合并：推导出的矩形 `cut`（无旋转、同 `part`、同 `z`）两两最近距离 < 0.8 mm 时合并为外包矩形，
  `id`/`source` 用 `+` 连接。手写进 spec 的特征不受影响。

## 栅格与网格

- XY 栅格覆盖外轮廓包围盒外扩 1 格，格点间距 `resolution`；Z 分层取所有特征 Z 边界的并集。
- 每格每层是否为实体：先算壳体（壁 + 底 + 顶 + 止口），再并入 `add`，最后减去 `cut`；
  上下壳分别计算，`split` 以下归下壳，以上归上壳（止口按规则跨越分割面）。
- 输出：实体格与空格之间的每个面，同一平面同一朝向的面贪心合并成矩形。相邻矩形落在
  某矩形边上的角点会插入该边，再从矩形中心扇形三角化，消除 T 形接点。
- 自检：每条有向边必须恰好有一条反向边（闭合、定向一致的流形）；只在棱上相接的两个实体格
  会违反这一条，此时判失败并提示调整。按散度定理算网格体积，必须等于实体格数 × 单格体积
  （相对误差 < 1e-6）。

## 检查

| 检查 | 结论 |
|---|---|
| 器件立体包围盒 ∩ 实体 | 有交集即失败，列出器件与位置 |
| PCB 板体（板框 × 板厚）∩ 实体 | 有交集即失败（螺丝柱只接触板面） |
| 壁/底/顶厚 < 1.5 mm | 警告 |
| 沉头深度 ≥ 板底高度、有螺丝柱时分割面不在板厚内 | 参数校验失败 |
| 非流形边、网格体积 ≠ 栅格体积 | 失败 |

## 输出

`--out` 必须是新目录：`bottom.stl`、`top.stl`（二进制 STL，mm）、`manifest.json`
（`status`、spec 哈希、各部件包围盒、三角形数、体积、检查明细、warnings）。失败时只写 manifest。

## 测试

- 单元：矩形/圆/旋转矩形栅格化；距离环带；螺丝展开；体积自检；STL 字节格式。
- 构建：最小矩形板生成上下壳，体积与解析值一致；侧开口让开越界连接器；
  器件过高时干涉失败；未知字段与非法参数拒绝。
- 真板：ceshi `pcb dump` + `pcb 3d models` 跑 `plan → build`，检查通过并在切片软件中打开。

## 真板记录（ceshi，2026-10-23）

`pcb dump` + `pcb 3d models`（23 件）→ `plan --top-cutout SW1,SW2,J1 --height J1=8.5,U1=0.9,X1=0.8`：
总高 18.1 mm，分割 6.6 mm，特征 4 个（J1 侧开口 + 3 个顶面开孔），无安装孔（警告）。
`build`（0.1 mm，约 1 s）四项检查全过；独立脚本按 float32 坐标重算：下壳 2444 / 上壳 2644 个
三角形，坏边 0，体积 3584.8 / 3939.5 mm³ 与 Go 侧一致。预览可见 SW1/SW2 开孔间只剩约 0.26 mm 隔条。


## 推盖盒（`type: slide`）

- 只支持 `outline.mode: rect`；`split`、`lip` 必须为 0/空。
- 导槽 Z 范围 `[H - retain - lid - 2·gap, H - retain]`，在四壁内侧挖深 `groove`；入口一侧
  （外形包围盒边以外、`slide.side` 方向）在导槽底以上全部敞开。从外形边而不是腔体边开口，
  外圆角整段被让开，侧壁以完整壁厚收口，不会留下细边。
- 盖板 `z ∈ [导槽底 + gap, 导槽顶 - gap]`，平面范围为腔体外扩 `groove - gap`，在入口一侧延伸到外壁。
- `plan` 取 `groove = wall / 2`、`gap = 0.2`、`retain = 1`，总高另加 `2·gap + retain`。
- 螺丝柱只有下壳柱 + 盲导孔（螺丝自上而下穿过 PCB）。
- `slide-path` 检查：沿滑出方向，盖板任一格前方不得有盒体格，盖板与盒体也不得重叠。

## 卡扣

- 凸点：下壳止口外侧环带 `(c + (w−gap)/2, c + (w−gap)/2 + snapDepth]`，以卡扣点为中心 `w × w` 方框内，
  `z ∈ [split + 0.25·lip.height, split + 0.75·lip.height]`。
- 窗口：上壳同一位置贯通外壁（方框和 Z 各放大 `gap`）。不用盲槽，盲槽会让外壁只剩约 0.4 mm。
- `plan --snaps auto`：上下壳且无螺丝柱时，取外形与包围盒四条中线的最外交点，再外推 `clearance + wall/2`。
- 检查：每个卡扣在下壳和上壳都必须改变至少一格，否则判失败（点没落在壁上）。

## 预览与 STEP

- `preview.png`：等轴测 z-buffer 着色渲染，第一件原样，第二件绕 X 轴翻转后并排；纯 Go 标准库。
- `--step`：每件一个 AP214 文件，`FACETED_BREP` 的每个面是合并后的平面多边形
  （`FACE_SURFACE` + `PLANE` + `POLY_LOOP`）。测试校验所有引用可解析、面数等于多边形数；
  尚未用 CAD 软件导入验证。

## 真板记录 2（ceshi，2026-10-23）

同一回读、`--top-cutout SW1,SW2,J1 --height J1=8.5,U1=0.9,X1=0.8`：
- 上下壳（自动 4 个卡扣）：全部检查通过；下壳/上壳 2578/2856 三角形，体积 3592.3/3907.8 mm³，坏边 0。
  警告：上壳 SW1/SW2 开孔间隔条细于 0.8 mm。
- 推盖盒（`--type slide`，x+ 出盖）：总高 19.5 mm，`slide-path` 通过；盒/盖 1314/444 三角形，
  体积 6035.3/1852.7 mm³，坏边 0。警告：入口拐角处的顶沿和盖板隔条细于 0.8 mm。

## 真板记录 3（ceshi，2026-10-23，开孔合并 + 推盖入口修正后）

同样参数：SW1/SW2 开孔合并为 `SW1-opening+SW2-opening`。上下壳与推盖盒均检查全过、**零警告**；
独立核对：下/上壳 2578/2820、盒/盖 1228/354 个三角形，坏边全为 0，体积与 Go 侧一致。

