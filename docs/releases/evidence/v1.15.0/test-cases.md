# v1.15.0 有限用例

以下 ID 只定义本次用户批准的有限范围；不沿用完整电路验收中的同名判据。
全部用例在候选 `9fda0e2b` 上执行；现场写入只针对 ceshi 专用工程。M1 后 F1→E1→F2→G1→L1→N1→R1 为同一轮，R1 为最后一步。

| ID | 范围 | 输入、步骤与判据 |
|---|---|---|
| M1 | 软件与候选绑定 | 候选上运行 Go 全仓、连接器测试与 typecheck、发布脚本测试、agent-check、skill-check，全部 0 失败；现场 CLI 与 daemon 是同一个候选二进制（`vcs.revision` 为候选、`vcs.modified=false`，验收轮前后哈希不变），连接器由候选源码构建并热重载为 1.15.0。 |
| F1 | PCB 器件 3D 模型 | PCB1 上 `pcb 3d models` 取基线；`pcb 3d transform --ref R1 --offset-z 1` 先 dry-run 再写入；save → doc reload 后清单只有 R1 变化；`pcb 3d bind` 把 R1 换成 C0402 模型，save → reload 后只有 R1 模型变化且摆放保留；再换回并恢复摆放，save → reload 后清单与基线逐字段相同。每次写入即时回读 verified。 |
| E1 | 3D 导出 | 用 `pcb export-3d --format obj` 在基线、改摆放后、换绑后各导出一次：改摆放后恰好一个器件的顶点整体 +1 mm（Z），其余顶点不变；换绑后该位置模型高度改变。`pcb 3d export-shell` 在无外壳的板上明确报错且不写文件。 |
| F2 | 参数化外壳 | `pcb dump` 回读每个焊盘带钻孔可用性（J1 读到 1.1 mm 金属化孔）；`pcb 3d models` 后用同一参数分别 `pcb enclosure plan` 上下壳与推盖（`--top-cutout SW1,SW2,J1 --height J1=8.5,U1=0.9,X1=0.8`），J1 不被当作螺丝柱，SW1/SW2 开孔合并；`build --step` 两种类型检查全部通过、零警告；独立脚本核对四个 STL 坏边 0、体积与工具一致。 |
| G1 | 多原理图撞号 | 先用 `schematic.pages.list` 回读页与原理图的归属；ceshi 两套原理图都有 R1/R2 且网络不同；分别在两套原理图的页上 `sch list --include-pins`：各自读到本原理图的网络（Schematic1 为 3V3/EN/IO0，Schematic2 为 TEST_*），不为空、不标 netAmbiguous。 |
| L1 | 工程结构 | `board new --name REL1150`：名字以新回读为准，未生效时结果为 partial、带 `notApplied:["name"]`、`boardName` 为实际名字、文档经回读存在、CLI 非零退出；`board rename` 只有回读出现新名才成功，否则非零退出；`pcb doc-delete` 拒绝删除仍绑定组合的 PCB；`board delete --dry-run` 列出将删文档，`board delete` 删除 PCB 与原理图、residual 为空、刷新工程树。 |
| N1 | 宿主与编辑器 | health 列出三个窗口并都识别为 edadesk；`editor panels` 收起与展开均退出 0，锁定侧栏如实给出警告、不擅自解锁。 |
| R1 | 收尾状态 | 该轮最后一步：文档集合与组合列表与本轮开始时一致（Board3 已删除，当前文档标记位置可以不同）；PCB1 的 3D 模型清单与 F1 基线逐字段相同。 |
| E2E | 延期 | 完整设计 E2E（需求到成品，含 PCB 布线与制造验收）本轮未执行；ESP32-S3 芯片级 N8R8 完整流程（#43）仍为 in-progress，留待 follow-up。 |
