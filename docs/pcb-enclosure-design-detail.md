# PCB 参数化外壳：实现细节

主文档见 [PCB 参数化外壳设计](pcb-enclosure-design.md)。

## 坐标

- 单位 mm，XY 与 PCB 相同（y 向上），由回读的 mil 乘 0.0254 换算。
- `z = 0` 是下壳外底面。板底 `zBoardBottom = floor + standoff`，板面 `zBoardTop = zBoardBottom + board.thickness`。
- 外壳总高 `H = zBoardTop + maxTopHeight + topClearance + lid`；`split` 默认等于 `zBoardTop`。

## 参数文件 `spec.json`（version 1）

```json
{
  "version": 1, "unit": "mm", "resolution": 0.1,
  "source": {"boardSha256": "...", "project": "ceshi", "documentUuid": "..."},
  "outline": {"mode": "board|rect", "clearance": 0.5},
  "wall": 2, "floor": 2, "lid": 2,
  "board": {"outline": [[x,y],...], "thickness": 1.6, "standoff": 3},
  "height": 20, "split": 6.6,
  "lip": {"height": 2, "gap": 0.2},
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

