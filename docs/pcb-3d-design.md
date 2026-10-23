# PCB 3D 建模：基础 CLI 设计

目标：基于已有的 PCB，让 Agent 能看清每个器件的 3D 模型状态、按参数修正模型摆放、
补绑缺失模型，并导出整板与外壳模型，全部走类型化动作与回读。字段、协议与测试见
[详细稿](pcb-3d-design-detail.md)。

## 官方接口支持情况（2026-10-23，EasyEDA Pro 4.1.78 / pro-api-types 0.4.25）

| 能力 | 官方接口 | 结论 |
|---|---|---|
| 3D 模型库增删查、复制、器件关联 | `lib_3DModel.*`、`lib_Device.modify` | 已实现（`lib model3d`、`lib device set-model3d`） |
| 读器件实例绑定的模型 | `IPCB_PrimitiveComponent.getState_Model3D` | 可读，现场已验 |
| 读/改器件实例的模型摆放 | `otherProperty["3D Model Transform"]` | 可读可写，现场已验；9 个数（mil/度） |
| 改器件实例绑定的模型 | `otherProperty["3D Model"]`、`["3D Model Title"]` | 无专用接口；经 `otherProperty` 写入，现场已验（含导出几何） |
| 整板 OBJ/STEP 导出 | `pcb_ManufactureData.get3DFile`（beta） | 已实现（`pcb export-3d`） |
| 3D 外壳导出 | `pcb_ManufactureData.get3DShellFile`（beta） | 可调用；未设计外壳时返回空 |
| 3D 外壳设计 | 外壳层 53–55 | 不支持：类型不含外壳层，现场画线/填充均被宿主拒绝；改为离线自研，见 [参数化外壳](pcb-enclosure-design.md) |
| 3D 外壳下单 | `place3DShellOrder` | 不做：只有交互式检查，且属于下单 |
| 3D 预览截图 | `PCB_3D_PREVIEW` 标签 + `getCurrentRenderedAreaImage` | 规划：没有打开 3D 预览的接口，只能截已打开的标签 |
| 光追渲染配置 | `pcb_RayTracerEngine`（beta） | 暂不做：配置是无类型对象 |

## 命令（`easyeda pcb 3d`）

| 阶段 | 命令 | 作用 | 写入 |
|---|---|---|---|
| 1 | `pcb 3d models [--missing]` | 列出每个器件的模型、摆放、高度属性和覆盖率 | 否 |
| 2 | `pcb 3d transform --ref J1 --offset-z -3 --unit mm [--dry-run]` | 只改指定的尺寸/旋转/偏移分量，其余保持 | 是 |
| 3 | `pcb 3d bind --ref U1 --model <uuid> --model-library <lib> [--dry-run]` | 给一个器件实例换绑或补绑库中已有模型 | 是 |
| 4 | `pcb 3d export-shell --format stl\|step\|obj --window <id> --out <新目录>` | 导出 GUI 中已设计好的外壳 | 否 |
| — | `pcb export-3d`（已有） | 整板 OBJ/STEP | 否 |

## 核心规则

- 写入只改点名的键：读当前 `otherProperty`，排除投影键，只替换目标键并重申位号；
  回读逐键比对，其他键或位号有变化即报 `partial` 并保留写前值，不重试。
- 摆放修改按分量合并：没给的分量保留原值，不重置为 0。
- 写入后先即时回读；持久性按惯例 `pcb save → doc reload → pcb 3d models` 确认。
  摆放语义的最终证据是导出模型的几何变化，而不是属性字符串。
- 外壳与预览不走 GUI 兜底；接口返回空时明确报“未设计外壳/未打开预览”。

## 实施顺序

1. `models`（只读）→ 2. `transform` → 3. `bind` → 4. `export-shell`。
每步完成连接器单测、Go 单测，并在 `ceshi` 上现场回读后再进入下一步。

当前状态（2026-10-23，ceshi，Web 4.1.78）：四个命令已在现场验证。`models` 全量 23 件读取；
`transform`、`bind` 经保存 → 重载 → 回读确认只有目标器件变化，导出 OBJ 证实几何随之改变，
并已恢复原值、回读与基线一致；`export-shell` 只验证了未设计外壳时的报错（接口无法创建外壳）。
摆放 9 个数中只有偏移 Z 经几何严格验证，其余分量仍是推断。
3D 预览截图和外壳设计等官方开放接口后再立项。
