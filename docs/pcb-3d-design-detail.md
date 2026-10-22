# PCB 3D 建模：实现细节

主文档见 [PCB 3D 建模设计](pcb-3d-design.md)。

## 现场观测（ceshi，2026-10-23，只读探测）

- `getState_Model3D()` 返回 `{libraryUuid, uuid, name}`；`uuid` 可能是 `模型UUID|库UUID`
  复合形式，归一时取 `|` 前一段，原值保留在 `raw`。
- 同一信息也出现在 `otherProperty`：`3D Model`（`uuid|lib`）、`3D Model Title`（名称）、
  `3D Model Transform`（9 个逗号分隔的数）。
- `3D Model Transform` 按样本推断为
  `尺寸X,尺寸Y,尺寸Z,旋转X,旋转Y,旋转Z,偏移X,偏移Y,偏移Z`，长度单位 mil、角度单位度：
  C0805 为 `78.74,51.181`（2.0×1.3 mm）；插件排针偏移 Z 为 `-118.11`（-3 mm）；
  贴片天线旋转 X 为 `180`；所有样本尺寸 Z 为 `0`（推断为“按模型原高”）。
  与官方外部库 `adjustment {size, rotation, offset}` 结构一致。映射标为 `inferred`，
  以导出 OBJ 的顶点位移验证后改为 `verified`。
- `otherProperty.Height`：部分器件有（如 `1.3mm`），供官方“自动生成缺失模型”使用。
- `get3DShellFile('shell','stl')` 在未设计外壳的板上返回 `undefined`。
- 当前板 `getCurrentPhysicalStackingConfigurationName()` 无自定义层叠时为空。

## 动作

### `pcb.model3d.list`（只读）

输出 `components[]`：`primitiveId, designator, layer, footprint{uuid,libraryUuid,name},
model{uuid,libraryUuid,name,raw}|null, status(bound|unbound|unknown),
transform{raw,size{x,y,z},rotation{x,y,z},offset{x,y,z}}|null, transformStatus(ok|missing|malformed),
height|null`；`summary{total,bound,unbound,unknown}`；`transformUnit:"mil"`、
`rotationUnit:"deg"`、`transformMapping:"inferred"`。读不到的器件计入 `unknown`，不从分母排除。

### `pcb.model3d.set_transform`（写）

输入：`designator`（唯一匹配）、`patch{size?,rotation?,offset?}`（各轴可选，mil/度）、`dryRun`。
步骤：
1. 按位号在 `getAll()` 中唯一匹配；0 或多于 1 个即拒绝。
2. 当前摆放必须是 9 个有限数，否则拒绝（不猜默认值）。
3. 合并分量，生成新串（数值保留至多 4 位小数）。与当前相同即 `noop`。
4. `modify(id, {designator, otherProperty: 当前键去投影键 + 新摆放})`。
5. 新鲜 `get(id)` 回读：目标键等于写入值、位号不变、其余非投影键不变才 `verified`；
   否则返回 `partial` 与写前 `before`，不重试。

### `pcb.model3d.bind`（写，阶段 3）

输入：`designator`、`model{uuid,libraryUuid}`、`dryRun`。先 `lib_3DModel.get` 确认模型存在并取名称，
写 `3D Model=uuid|lib`、`3D Model Title=name`，回读 `getState_Model3D()` 必须指向新模型。
若宿主忽略该写入，标 `unsupported` 并改走器件库关联（`lib device set-model3d`）后从库更新。

### `pcb.export.3d_shell`（只读，阶段 4）

输入：`fileType stl|step|obj`、`fileName`。返回空即 `INVALID_STATE`“当前 PCB 未设计 3D 外壳”。
文件上限与传输沿用 `pcb.export.3d`（20 MiB、artifact base64），CLI 写入新目录并校验 SHA-256。

## 测试

- 连接器：摆放串解析/格式化往返；畸形串拒绝；位号 0/多匹配拒绝；只改目标键；
  回读其他键变化报 `partial`；dry-run 不调用 `modify`。
- Go：命令参数校验（单位换算、至少一个分量、互斥项）；动作已注册且写动作进入验证写入名单。
- 现场：`ceshi` 上 `models` 全量读；`transform` 改一个器件后 save → reload → 回读，
  再恢复原值并回读；导出 OBJ 前后比较该器件顶点位移。
