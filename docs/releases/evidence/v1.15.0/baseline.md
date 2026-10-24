# v1.15.0 有限验收基准

## 用户范围决定

2026-10-24，用户问「可以发 release 了吧」，被告知 v1.14.0 之后连接器有新动作、版本规则要求 minor（v1.15.0），
中版本需现场验收与独立复核，且 dev CI 因 Skill 版本不一致而失败；用户随即选择「按 v1.15.0 有限验收范围开始发布
准备：先修 CI 版本不一致，再写 next-release、跑现场验收、独立复核，最后再问我是否执行 make release」，
其后在验收中途要求「请再试一次」。本次按 `cli-fixes` 发布，`full-design-e2e` 延期至 `follow-up`，完整设计 E2E
不补签。该决定只绑定本版本，`make release` 仍需用户另行明确批准。

## 候选与输入

产品源码候选为 dev 提交 `9fda0e2b`（v1.14.0 之后 43 个提交）：`e26c821f` 统一版本为 1.15.0 并修复 CI；
`aa806afa` 与 `9fda0e2b` 修复本次验收发现的组合改名误报。连接器运行时自 v1.14.0 起有改动（新增
`pcb.model3d.*`、`pcb.export.3d`、`pcb.export.3d_shell`、`board.new`、`pcb.document.delete`、`editor.panels`、
`pcb.route.auto`，焊盘回读带钻孔与金属化，网表导出后连续空闲等待，上报宿主 UA），按版本规则递增 minor；
CLI/daemon、连接器、npm/lock 与 Skill 统一为 1.15.0。

现场 CLI 与 daemon 是同一个候选二进制：在只含已提交文件的工作区构建（发布文档与未跟踪的证据目录临时移出），
`go version -m` 为 `vcs.revision=9fda0e2b…`、`vcs.modified=false`，版本串 `v1.14.0-43-g9fda0e2b`，SHA256
`8b769e2b5c575f4c89030d64184d95e88c8c8e0df90e31a432b739781f3072b4`。该二进制放在 PATH 之外（共用工作区的
其他会话运行 `make dev-build` 会改写 PATH 上的 CLI，见报告），daemon 由同一文件以分离进程启动。
连接器由同一候选构建（1.15.0，`dist/index.js` SHA256 `c0968cc48c03305525b39cd275c7838d75cec5846cd110e2d6f254aecfaf1034`），
经 IndexedDB 热重载装入；验收轮开头 health 记录 daemon 版本串与 CLI 相同、ceshi 窗口 connectorVersion 1.15.0、宿主 edadesk。

现场只写获准的 ceshi 专用测试工程（PCB1 与本轮自建并删除的 Board3）。开始状态：两套原理图——Schematic1
（页 cb1db930、d80de8c0）与 Schematic2（页 efb1d2f3），R1/R2 两边同号不同网；PCB1 为前序 ESP32 测试板
（23 个器件、模型全部绑定、无安装孔）。同一 edadesk 还开着另外两个工程窗口（不属于本轮），只读取其 health。
工程身份、原始日志与本地路径不公开。

## 环境与阈值

macOS arm64，edadesk 中的 Web EDA 4.1.78。用例 ID 含义以本目录 test-cases.md 为准，不是完整设计用例的补签。

阈值：写入后即时回读 verified；save → doc reload 后全量模型清单只有目标器件变化；导出 OBJ 的顶点变化与写入
严格对应（平移量逐点一致）；恢复后清单与基线逐字段相同；外壳两种类型 build 检查全部通过、零警告，独立脚本核对
STL 坏边为 0、体积与工具一致；工程结构操作后文档集合与组合列表回到开始状态，无残留；宿主拒绝或谎报的写入
必须以回读为准表现为 partial / 非零退出，不得报完全成功。

完整 B00–B10、完整设计 E2E 与制造验收不在本次通过范围；官方自动布线、泪滴、铺铜等改动以其提交时的现场记录
与自动测试为准，本轮未在板上重复执行。
