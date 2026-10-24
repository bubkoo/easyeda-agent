# v1.15.0 有限验收报告

用户于 2026-10-24 要求按 v1.15.0 有限范围准备发布。本报告使用 schema 3，通过范围为 cli-fixes；
完整设计 E2E 延期至 follow-up。有限软件通过不代表电路或制造通过。产品源码绑定 dev 提交 `9fda0e2b`。
最终轮：M1 于 2026-10-24 08:53:19–08:55:49 UTC，现场用例 08:56:09–08:57:07 UTC 一次连续执行，无续跑、无补救。

## 运行经过

共四轮，前三轮的问题都已修复或排除，结果只取第四轮；各轮原始输出分目录保留。

- 第一轮（候选 `e26c821f`，2026-10-23 16:22–16:26 UTC，`run1-e26c821f/`）：F1/E1/F2 通过；L1 发现缺陷——
  `board new --name REL1150_TMP` 建出 Board3（宿主 `modifyBoardName` 返回 false），结果却报 `partial:false`、退出 0；
  `board rename` 被拒时 `ok:false` 也退出 0（退出码见当时终端输出，未落盘）。该轮还做了复现实验：再建一个无名组合、
  对它两次改名（带下划线与普通名字，均被宿主拒绝）后删除；按名字 REL1150_TMP 的 delete-dry/delete 因找不到而退出 1，
  随后按实际名 Board3 删除，两个实验组合都已删除、残留为零。修复为 `aa806afa`。
- 第二轮（候选 `aa806afa`，16:30–16:37 UTC，`run2-dirty-cli/`）：用例按判据通过，但独立复核指出 16:31:09 UTC
  PATH 上的 CLI 被一次 `make dev-build` 改写为 `-dirty` 构建（`vcs.modified=true`，共用工作区的其他会话触发），
  F1–R1 由它执行；另有一次脚本解析失败后的续跑。此轮整体作废、不计入结果。
- 第三轮（候选 `aa806afa`，干净 CLI，16:47–16:48 UTC，`run3-lying-rename/`）：board.new 回报 `boardName` REL1150
  而回读组合仍叫 Board3——当时的代码在宿主返回 true 时才采用新名，由此推断宿主这次返回了 true（该版本未记录宿主原话）；board.new 虽报 partial、非零退出，却把未生效的 REL1150 写成 `boardName`，随后脚本用这个名字执行的 rename
  被宿主拒绝；doc-delete 因 expected-name 为空（`PCB name mismatch: expected "None"`）在预检被拒；delete 报
  `Board "REL1150" was not found`；均未生效；Board3 已按实际名删除，残留为零。修复为 `9fda0e2b`：两条改名路径都以
  新回读的组合列表为准，宿主原话记入 `hostReturned`。
- 第四轮（候选 `9fda0e2b`）：CLI 与 daemon 用同一个放在 PATH 之外的干净候选二进制，避免再被 dev-build 改写；
  连接器由同一候选构建并热重载。下表为该轮结果。

## 已完成的软件验证

- Go 全仓：16 个包全部 ok；顶层测试 2,936 项通过、0 失败、1 项跳过。
- 连接器测试 1,223 项通过、0 失败；typecheck 退出 0。
- 发布脚本测试 123 项通过；agent-check 通过；skill-check：192 个 tracked 文件通过。
- M1 各命令的退出码打印在执行终端（go/connector/tsc/scripts/agent/skill 均为 0），未单独落盘；落盘日志与之相符
  （m1-go.json 无失败、m1-tsc.log 仅一条 npm 配置警告、m1-agent.log 与 m1-scripts.log 末尾 OK）。M1 运行时工作区只有
  未提交的发布说明与未跟踪的证据目录，不影响被测源码。
- 现场 CLI/daemon：同一二进制，`vcs.revision=9fda0e2b…`、`vcs.modified=false`，版本串 `v1.14.0-43-g9fda0e2b`，
  SHA256 `8b769e2b5c575f4c89030d64184d95e88c8c8e0df90e31a432b739781f3072b4`，验收轮开始与结束各记录一次，哈希不变。
- 连接器 1.15.0，`dist/index.js` SHA256 `c0968cc48c03305525b39cd275c7838d75cec5846cd110e2d6f254aecfaf1034`
  （注入回执 `connector-inject-9fda0e2b.json`）；验收轮开头 health：daemon 版本串同上，三个窗口均为 1.15.0、edadesk。
- 五平台交叉构建与资产核对由 release-build 执行，不代表其他平台运行时验证。

## 用例结果

| ID | 结果 | 回读与证据 |
|---|---|---|
| M1 | pass | Go 2,936 通过 0 失败（16 包）、连接器 1,223 通过 0 失败、typecheck 退出 0、发布脚本 123 项、agent-check、skill-check 192 文件全部通过；现场 CLI 与 daemon 为同一 vcs.modified=false 的候选二进制（版本串 v1.14.0-43-g9fda0e2b，轮前后哈希相同），ceshi 窗口连接器 1.15.0。 |
| F1 | pass | 基线 23/23 已绑定；R1 摆放 dry-run 与写入均为 `…,0,0,0`→`…,0,0,39.3701`，写入 verified；save:true、doc reload 后全量清单只有 R1 变化；换绑 C0402 verified，重载后只有 R1 模型变化且偏移保留；换回并恢复摆放均 verified，save → reload 后清单与基线逐字段相同。 |
| E1 | pass | 基线 OBJ 10,606 个顶点；改摆放后恰好 195 个顶点变化，全部下移 1 mm 后与原顶点逐点一致；换绑后该位置旧顶点 195 个（z 0–0.36 mm）换成 318 个（z 1.0–1.5 mm，含 1 mm 偏移）；export-shell 报「This PCB has no 3D shell」且未写文件。 |
| F2 | pass | 111 个焊盘全部带 hole 可用性，J1 四脚 ["ROUND",4.3308,4.3308]（1.1 mm）、金属化、带网络，未生成螺丝柱；上下壳高 18.1 mm、4 个卡扣、开孔 SW1+SW2 合并，推盖高 19.5 mm；两次 plan 都提示「no mounting holes or --pillar」（ceshi 无安装孔）；两种 build 全部检查通过、零警告、含 STEP；独立核对 bottom/top/box/lid 三角形 2578/2820/1228/354，坏边全 0，体积 3592.3/3906.2/6030.7/1851.2 mm³ 与工具一致。 |
| G1 | pass | pages.list 回读：页 cb1db930、d80de8c0 属 Schematic1，页 efb1d2f3 属 Schematic2；Schematic1 页读到 R1 = 3V3/EN、R2 = 3V3/IO0；Schematic2 页读到 R1 = TEST_LINK/TEST_A、R2 = TEST_B/TEST_LINK；两边各按本原理图网表，均不为空、均无 netAmbiguous。 |
| L1 | pass | board new 返回 boardName Board3（requestedName REL1150、notApplied ["name"]、partial true、verified true，警告说明宿主拒绝改名、回读仍为 Board3），CLI 末行报未完全验证并非零退出；board rename 返回 ok false、verified false、hostReturned false，非零退出；doc-delete 拒绝删除绑定在 Board3 的 PCB3；dry-run 列出 PCB3、Schematic3；delete 删除二者，residual 为空，treeRefreshed true；组合列表 Board1/2 → Board1/2/3 → Board1/2。 |
| N1 | pass | health 列出 ceshi、ESP32-C3 开发板与 MIPI 扩展板三个窗口（后两个不属于本轮，只读），连接器均 1.15.0、宿主全为 edadesk；editor panels 收起、展开均退出 0，回报 close/openLeftPanel、close/openRightPanel 调用与锁定状态，左侧锁定时给出警告且不解锁（stateReadable false）。 |
| R1 | pass | 该轮最后一步：doc ls 的文档集合与本轮开始时相同（当前文档标记从原理图页回到 PCB1），组合列表仍为 Board1、Board2；PCB1 的 3D 模型清单与 F1 基线逐字段相同；收尾 health 中 daemon 版本串不变、ceshi 连接器 1.15.0。 |
| E2E | not-run | 完整设计 E2E（需求到成品、PCB 布线与制造验收）本轮未执行，留待 follow-up；ESP32-S3 芯片级 N8R8（#43）PCB 阶段仍为 in-progress，不补签完整设计通过。 |

## 现场回读

- 原始输出保存在本地忽略目录 `artifacts/release-v1.15.0/`：执行脚本 round2.sh、f1.sh、f2.sh、rest.sh，日志 round4.log、
  m1-times.txt，M1 输出 m1-*，逐步 JSON（f1-*、f2-*、g1-*、l1-r-*、n1-*、r1-*）、导出的 OBJ 与外壳 STL/STEP、
  预览图与 manifest、round-health-start/end.json、connector-inject-9fda0e2b.json；分析脚本 f1_check.py、stlcheck.py、
  summary.py 及其输出 summary.log。前三轮分别在 run1-e26c821f/、run2-dirty-cli/、run3-lying-rename/。
- 每次写入后的回读、save 回执与 doc reload 回执逐步保存；OBJ 顶点差分与 STL 坏边/体积由独立脚本从导出文件重新计算。

## 已知限制与说明

- Web 4.1.78 的 `modifyBoardName` 对新建组合返回过 false，也返回过 true 而名字未变：`board new --name`、`board rename`
  现以回读为准如实失败，改名能力本身待查。
- 外壳螺丝柱的正向识别仅由单测覆盖（ceshi 无安装孔）；STEP 只做引用完整性自检，未经 CAD 导入；外壳未实际打印。
- 3D 摆放 9 个数中只有偏移 Z 经几何严格验证，其余按样本推断。
- 官方自动布线、泪滴、铺铜、rip-up/clear、网表空闲等待、桌面本地库修复等改动本轮未在板上重复执行，以其提交时记录为准。
- 共用工作区时，其他会话的 `make dev-build` 会改写 PATH 上的 CLI；发布验收应使用 PATH 之外固定哈希的二进制。
- 第四轮期间审计日志里另有不属于本轮脚本的只读调用（08:52 的 sch status / pcb dump；08:57:03 一次 sch status 因
  ACTION_BUSY 被拒），未对 ceshi 写入，不影响结果。

## 独立复核

两次复核均由未参与执行的独立 Agent 只读完成，直接从原始产物重新推导结论，不依赖 summary.log。

- 第一次（针对第二轮材料）：fail。阻断项：PATH 上的 CLI 在 16:31:09 UTC 被 dev-build 改写为 `-dirty`（`vcs.modified=true`），
  与「版本串不带 dirty」的判据字面不符；「未写入任何东西」缺少证据引用（复核者以审计日志确认确无写入）。
  非阻断：G1 页与原理图的归属是名称推断（复核后回读发现确实写反，已改为 pages.list 证据），R1「逐行一致」措辞不准确，
  plan 的无安装孔提示未提及。处理：作废第二轮，改用 PATH 之外的干净二进制重跑；后续又在第三轮发现并修复改名谎报。
- 第二次（针对第四轮与本材料）：**pass**，无阻断项。复核者独立核实：`9fda0e2b` 与 43 个提交；ez9 的 SHA256、
  `vcs.revision=9fda0e2b`、`vcs.modified=false` 与 round4.log 首尾一致；daemon 为同一二进制（进程命令行为该文件，
  三次 health 的 pid 与版本串相同）；连接器包哈希与注入回执一致；从 m1-go.json 重数 Go 顶层 2,936 通过 / 1 跳过 / 0 失败、
  连接器 1,223 / 0；从三个 OBJ 压缩包重算 195 个顶点 Δz=+1 mm 及换绑 195→318；重写脚本重算四个 STL 的三角形数、
  坏边 0 与体积；G1、L1、N1、R1 的字段、退出码与组合列表均与报告相符；各轮披露与对应目录、审计日志相符，E2E 如实为 not-run。
  非阻断意见（M1 退出码未落盘、第三轮两处措辞、第一轮额外实验、并发只读客户端）已在本报告相应位置补充说明。
