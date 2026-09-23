# new-api 上线前验收

## 2026-09-23 正式全量副本演练 v2：完成；正式未变更

- 本节取代下方“最新快照未完成”的当前状态描述，旧记录仅为历史。单次只读一致性导出正式37表，包含 **3,822,043条完整历史日志**，未截断历史日志；备份759,827,987字节，gzip/表数及SHA256 `481fbcc8b9db9d68bb13eafe0c3b2689e6a23f714724dc7d4b7edea56c82a80d`核验通过。备份、检查点、脚本和报告保留在 `backups/prod-rehearsal-20260923-v2/`，不再因等待导入而重新导出或删除副本。
- 独立 `new-api-rehearsal-v2-*` MySQL/Redis/应用及internal网络，应用未发布端口；正式站、原测试站均未重启。原媒体目录复制到副本并建立逐文件SHA256清单，媒体子目录只读挂载、运行日志目录可写。媒体复制与数据库非原子快照，不声称全部媒体在同一时点一致。
- 候选仍为 `sha256:e3fb524fb6ab98f02780917a41ccb32fbe1be563394fdb150f522873a9740238` / revision `33b297783252056ea133ad31b562e0d83a360d38`，来自Actions 35751632277。旧镜像启动→候选迁移→重复启动→镜像回滚→同一备份完整恢复→旧版启动→候选启动均通过；核心表选定字段哈希一致，原索引语义保留，恢复后schema/索引一致。启动检查约2秒，**不等于正式升级或完整恢复停机时间**；全量备份恢复约9分钟。
- **发现并补齐发布清单缺口：** 全量副本中的 `gpt-image-2` 仍是旧 `ModelPrice=0.1`，仅换镜像并迁移视频价格后，2K图片固定价格断言失败。已仅在副本通过定价管理API追加9个GPT/Gemini图片模型的显式分辨率计费表达式，复用之前测试站验证过的1K $0.10、2K $0.16、4K/未知 $0.20及有效图片数量规则，并保存原价/迁移计划。正式发布必须包含此步骤，不能只换镜像或仅迁移视频价格。
- 配置演练：Seedance渠道17改type61绑定aistarslab，30模型保持原按秒/按次单价，4个c49仅在副本暂禁用；Codex/Claude两条亲和规则显式prefer。价格重复迁移预览changed=0；多次重建后功能正常。副本管理审计保留。
- 真实HTTP/mock回归：GPT-5.5普通及SSE各经历主渠道500→备用200；GPT Image请求1K/2K/4K与n=2、Gemini原生图片1K/2K/4K扣费分别符合规则，生成8条归档，非法n=129为400且无上游调用/扣费；用户扣费、Token扣费、消费日志合计均561,100 quota。31次Seedance模拟提交覆盖30模型及1失败退款，用户/Token均79,130,000 quota，重复读取幂等；私网mock视频下载被安全策略拒绝为预期。未新增真实供应商费用。
- 16并发本地签名支付通知只入账3,385,000 quota且1条充值日志，错误签名/支付供应商拒绝；3个真实历史图片通过认证签名HTTP200下载，逐字节SHA256与复制文件一致，篡改签名401。
- Canvas/Video复用正式精确image ID、独立客户端数据和副本issuer。Playwright通过仅允许三个隔离域名的HTTP代理完成实际登录、回跳、会话API200和HttpOnly cookie检查；不再使用route.fetch代转发，避免额外cookie jar掩盖浏览器行为。范围为隔离HTTP，未验证正式HTTPS入口。
- 清理：测试业务用户/Token/订单/任务/日志/图片行及延迟用量统计已清理，OAuth会话/客户端/代理/隧道及临时凭据、新生成媒体已清理；所有五张核心表选定字段重新与原快照匹配。独立副本、原媒体副本、备份和报告保留，正式healthy/restart0及启动时间不变。
- 报告：服务器上述私有目录内 `final-report.json` 和分项报告；本地 `~/.local/share/new-api-github-main/prod-rehearsal-20260923-v2/reports.json`。边界：镜像单独回滚是在业务配置迁移之前验证；应用新配置后回滚还需恢复已保存的配置，或在冻结写入后按完整数据库恢复方案执行，不能在线覆盖上线后新增交易。正式同步仍需单独确认。

## 2026-09-23 最终候选集中终验：验收通过，未执行正式同步

- GitHub Actions 35751632277 构建并核验 ARM64 OCI 镜像：`ghcr.io/carminback/new-api@sha256:e3fb524fb6ab98f02780917a41ccb32fbe1be563394fdb150f522873a9740238`，源码 revision `33b297783252056ea133ad31b562e0d83a360d38`；测试站已按 digest 运行并 healthy，正式站未修改、未重启。
- 最低成本真实视频单已在该候选前一运行镜像完成：4 秒 Seedance 视频 HTTP 200 下载、MP4签名有效、用户与Token各扣600,000 quota且重复查询不重复扣费；没有再次提交付费单。最终候选的 AistarsLab c50 HTTP模拟覆盖4秒、2个c50模型、失败退款、超长时长400及清理，报告通过；当前供应商缺失的4个c49仍未启用。
- 正式数据库37表副本在隔离 internal Docker 网络完成六阶段演练：旧镜像启动、候选首次迁移、候选重复启动、仅镜像回滚、数据库恢复+旧镜像、候选OAuth准备均HTTP 200；核心 users/tokens/top_ups/tasks/image_generations 哈希一致，索引约束语义保留，恢复后schema精确一致。候选镜像与副本报告：`verification/final-release-20260922/migration-report.json`。
- 使用正式 Canvas 镜像 `ghcr.io/carminback/infinite-canvas:31ba972` 与 Video 镜像 `ghcr.io/carminback/video:sha-0a7f3de` 的隔离容器，连接副本 issuer/数据库，在本地SSH隧道上用Playwright完成真实浏览器OAuth：Canvas、Video均authorize 302、callback 302、客户端会话接口200、HTTP-only会话cookie及authenticated均通过。范围是HTTP隔离环境，不包含正式HTTPS入口；报告：`verification/final-release-20260922/browser-report.json`。
- 仓库全量 `go test -count=1 ./...`、定向vet、diff检查及最终回归均通过；AistarsLab c50定向测试通过。真实/模拟夹具、OAuth用户和隔离客户端已清理，延迟用量统计复核完成。
- 结论：已有隔离副本验收通过，但按用户要求重新建立“最新正式数据”副本时，生产导出在服务器侧耗时过长，随后导入/迁移脚本因大表导入等待未在本次窗口完成；该次副本已停止并清理，未影响正式站。不能把本次最新快照演练记为通过；正式发布仍应以已有已通过副本为依据，并保留最新快照复核项。

## 2026-09-22 续验：按正式站接入 AistarsLab 测试渠道

- 只读核对正式渠道17：Base URL `https://api.video.aistarslab.com/openai`，Bearer鉴权，34个去重Seedance别名映射到 `<线路号>:seedance-2.0[-fast]` / `seedance-2.5`，分辨率由公开模型别名及metadata传递；Grok为独立渠道19，不在本轮范围。
- 测试旧Key读取供应商配置得到业务401，正式当前Key得到code0。已仅在测试渠道17更新为当前Key，凭据不输出。实际供应商 `/openai/v1/models` GET返回200，共40个原始模型条目；没有提交真实付费视频。
- 当前供应商配置不列出49线路。正式的 `seedance-480p-c49`、`seedance-480p-fast-c49`、`seedance-720p-c49`、`seedance-720p-fast-c49` 均暂不启用，不能仅添加两个插件声明就认定可用。其余30个别名的上游模型和分辨率在当前配置中均存在。
- 测试渠道17已通过管理API改为type61，绑定 `setting.task_plugin_key=aistarslab`，名称 `AistarsLab-Seedance`，Video分组，保留正式30个模型的映射和各自按秒/按次单价。价格转换为 `tier("base", u("seconds") * price)` 或 `tier("base", u("videos") * price)`，没有应用供应商同步重新定价；重复迁移dry-run变更0。没有修改运行镜像或重启应用。
- 备份：`/opt/docker/new-api-rc20-test/backups/aistarslab-integration-20260922-a3edd6a8`，compose/runtime.env/渠道配置/逐模型旧价格及40表SQL，gzip/SHA256检查通过。回退时通过管理API恢复渠道17及保存的逐模型配置，并从私有备份恢复旧Key；不覆盖整个测试库。首次PUT误带status被接口拒绝，渠道未变，已先恢复价格并核对健康，再移除status重试成功。
- HTTP模拟验收：独立临时用户与分组、type61渠道，复用测试渠道17的完整30模型列表/mapping/setting。30模型各提交5秒请求，逐一确认上游映射、分辨率、字符串seconds、n=1及metadata；后台轮询均SUCCESS，另1请求FAILURE退款。用户和Token汇总扣费均79,130,000 quota，逐任务按正式单价/单位一致；重复查询不重复扣费，超长时长400。私网模拟结果URL被下载保护拒绝（502/artifact_request_rejected），未将其误报成视频供应商失败，也不声称公网CDN下载已验收。
- 历史兼容：原渠道17的platform=1既有成功任务，变更渠道类型后读取仍200/completed。该渠道不存在未完成任务。插件AistarsLab与TaskPricingMigration定向Go测试通过。
- 清理：临时用户/Token/渠道/任务/日志/用量统计/缓存已清理，额外清理延迟写入的30条夹具统计；既有用户/Token/充值/图片/任务选定字段与schema哈希均与本轮前相同。mock38991已关闭，compose/runtime.env字节未变，测试healthy/公网200，正式原镜像及启动时间不变。
- 限制：当前插件对c50仍使用旧的较保守限制（如最短5秒，部分输入数量和宽高比更窄）；本轮覆盖文生视频，未完整验证图片/音频/视频参考输入，也未提交真实收费任务或验证公网视频下载。c49去留及外部链路仍未关闭，所以本轮不代表全版本正式发布获准。
- 证据：`verification/aistarslab-integration-20260922/final-report.json`；本地副本 `~/.local/share/new-api-github-main/aistarslab-integration-20260922/final-report.json`。本轮无需修改插件源码，通过现有任务插件绑定完成当前可列出模型的接入。

## 2026-09-22 测试站正式升级验收：未通过

本轮只修改隔离测试站；正式站保持只读。候选的支付、OAuth、视频账本和重复启动检查通过，但复制正式视频渠道配置后出现可复现的路由不兼容，且正式现有两个模型无法迁移定价，因此当前不能直接同步正式站，也没有请求正式部署确认。

### 版本与环境

- 正式：revision `672f3da30286d790fad4a8c8ed5c603ffadfe888`，镜像 digest `68f712d0e6856d7394719a141be66b62ec520f309c10e490b3bd75c23074e503`。
- 候选：revision `3e86fa0c0da4558e4551925daf42d8f984ca5aea`，GitHub Actions `35697234070` 构建，镜像 digest `dc015eb6d0462e3826b84f16faccce3b9f75d5538c1dee81d435d109c2b2c227`；再次核对运行镜像为 ARM64、OCI revision 相符。
- 隔离环境：`oracle:/opt/docker/new-api-rc20-test`，MySQL 8.4.9 的 `new_api_rc20_test`，独立测试 Redis。生产与测试数据源不同。
- 完整差异为 1669 个文件，不能用调度验收替代整个升级版本验收。本次未修改业务源码。
- 备份：`backups/release-gate-20260922-02955c03`，含 compose、runtime.env、40 张表一致快照；`mysqldump --no-tablespaces --single-transaction --quick --skip-lock-tables --set-gtid-purged=OFF`，建表数量、gzip 和 SHA256 验证通过。

### 明确阻塞

1. **正式 Seedance 渠道的旧类型不兼容。** 正式 Video 渠道为 type=1。保留实际正式 Seedance 渠道的类型、setting、model_mapping，使用临时渠道及无真实供应商连接的地址，在候选请求 `seedance-720p-c47` 的 `/v1/videos` 返回 **503 / model_not_found**，提示模型由任务插件声明但没有可用渠道。余额未变化。源码 `middleware/distributor.go` 要求任务插件与渠道类型/显式绑定相符；AistarsLab 插件没有 type=1 绑定，不能靠换镜像自动延续旧选渠。需针对实际供应商接口评审渠道插件绑定及类型迁移，并在测试站复验。
2. **正式新增模型没有候选用量定义。** 正式当前 35 个 Video 模型中，`seedance-480p-c49`、`seedance-480p-fast-c49` 不在候选插件声明中。按正式 `/api/pricing` 的价格和按秒/按次单位生成只读迁移预览，整批及这两个模型逐项均返回 **400 / has no task plugin usage schema**。旧 2026-09-18 的 34 模型静态清单已不能作为当前正式迁移输入，必须按最新正式集合生成并验证，不得直接套用旧价格文件或测试站配置。

### 已通过及验证边界

| 检查 | 本轮证据 |
| --- | --- |
| 后端与前端 | `go test -count=1 ./...` 通过；relaykit 的 `GOWORK=off go test -count=1 ./...`、独立 build 通过；controller/service/model/middleware/router/relay 定向 vet 通过。前端 typecheck、170 文件 / 2105 个 Vitest 测试、生产 build 通过。未改源码，不重复此前已通过的调度 Race。 |
| MySQL 重复启动与完整性 | 临时 OAuth 配置重建及恢复原配置重建后，核心表选定字段和完整列定义哈希均与测试前相同：45 用户、62 令牌、29 充值订单、12 图片记录、82 视频任务。用户钱包字段均为 BIGINT。覆盖当前已升级 MySQL 的重复启动，不声称本轮重新完成正式旧库首次迁移或三库完整矩阵。 |
| Epay HTTP 通知 | 隔离测试库临时订单，16 次并发有效签名通知均正常响应，只入账一次、增加 3,385,000 quota，仅一条充值日志；无效签名和错误支付供应商订单均拒绝且不加余额。未创建真实付款订单，也未由外部支付平台发起通知。支付渠道 type 的变化与跨供应商订单混用不同，旧逻辑允许同一 Epay 订单记录平台返回的实际支付方式。 |
| OAuth | 临时独立 Canvas/Video 客户端经测试站 HTTPS 授权302、换码200；错误PKCE、重复兑换及错误回调地址400。Canvas能力为image/video/text/audio，Video为video。未访问正式客户端回调，也未验证真实浏览器应用回跳；后续重复授权测试触发现有429限流，未清除限流器。 |
| 视频完整链路 | 使用匹配 Sora 插件的临时 type=55 渠道和受控模拟上游，真实 HTTP 提交→后台轮询→终态持久化。成功4秒视频用户和Token各扣20,000 quota；失败视频退款后两者净扣费0；重复读取不重复结算。仅两次模拟上游提交，无真实付费供应商调用。不能据此宣称正式旧 type=1 Seedance 渠道兼容。 |
| 历史视频与图片读取 | 既有成功/失败视频均HTTP200，状态completed/failed。受控临时图片归档行引用既有PNG，签名读取200、篡改401、nosniff正确。测试库既有SUCCESS记录指向的文件缺失，而旧PNG归档行已过期，因此没有将本次签名测试误报成既有归档数据完全一致；既有媒体文件未改动。 |
| 清理与健康 | 临时用户、Token、授权码、会话、订单、日志、视频任务、渠道/ability、图片记录、用量统计和夹具缓存清理；测试定价恢复，管理审计保留。runtime.env及compose与备份逐字节一致。38991无监听，测试公网status200、healthy、restart=0。正式digest及启动时间 `2026-09-15T03:24:47.586858135Z` 未变化，healthy、restart=0。 |

证据：服务器 `verification/release-gate-20260922/final-report.json` 的 `release_ready=false`；同目录含视频路由、价格预览、支付、模拟视频、历史视频、签名图片和重启完整性报告。本地脱敏汇总位于 `~/.local/share/new-api-github-main/release-gate-20260922/final-report.json`。

下一步先补齐当前正式模型的插件支持，准备渠道绑定和保持原单价/单位的价格迁移，再在测试站按正式配置重验。通过后才能完成具体生产变更和回滚方案，并按用户要求询问是否同步正式站。正式授权仍未获得。本次保持MySQL；SQLite/PostgreSQL矩阵不作为仅MySQL发布的直接前置条件。

以下为历史验收记录，其中旧版本、180秒超时建议及“源码未冻结”等结论不代表当前候选状态；当前默认总时限600秒、首字节90秒、最小重试剩余5秒。

## parity10 本地上线前验收（2026-09-19）

结论：本地功能、生产模式后台运行、Linux ARM64 容器、反向代理和小规模并发回归均通过，未发现新的应用功能失败；尚不能直接放行正式上线。剩余门槛是源码冻结、真实外部服务联调及正式变更授权。本轮没有修改业务源码或正式服务。

## 验收对象

- 工作区：`new-api-github-main-local`，分支 `migration/prod-parity-3524fe0`，仍包含未提交的迁移改动。
- 本地服务：`http://127.0.0.1:3010`，版本 `github-main-20260919-3524fe0-parity10`。
- 本地 MySQL 正式数据副本与独立 Redis；应用仍使用 `LOCAL_VERIFICATION_MODE=true` 及出网隔离。
- 证据目录：`/Users/carmin/.local/share/new-api-github-main/readiness-20260919/`，目录权限 0700，日志及结果为私有文件。

## 本轮结果

| 验收项目 | 结果与边界 |
| --- | --- |
| 后端 | `go test ./...`、relaykit 独立构建与测试、`go vet ./...` 均通过；全量 Go 测试允许复用缓存。另以 `-count=1` 重跑支付、OAuth、任务计费等关键回归，通过。 |
| 并发与治理 | 文本超时/取消、恢复阶段、探测调度、有效输出计时等定向 `go test -race -count=1` 通过。 |
| 前端 | Vitest 153 文件、1916 测试通过；typecheck 通过。 |
| HTTP 与历史数据 | 首页、status、pricing 均 200；复制的令牌和管理员凭据可用；10439 条绘图历史及图片内容哈希核对通过，无签名返回 401，个人范围隔离通过。成功/失败历史视频各取一例，经复制的所属用户 Video 令牌查询均 200，状态分别为 completed/failed。 |
| OAuth 实际本地 HTTP | 使用临时用户和真实应用会话，Canvas/Video 授权返回 302、换码返回 200；错误 PKCE 和重复兑换均 400。Canvas 返回 image/video/text/audio 能力，Video 返回 video。未访问外部回调地址，未验证真实客户端浏览器回跳。修正此前未确认状态：当前本地两客户端密钥已配置，可完成本地换码。 |
| MySQL 充值幂等 | 对临时用户的同一小数额度 Epay 订单，16 个并发 `RechargeEpay` 调用只有 1 次入账、15 次识别重复，只有 1 条充值日志，额度精确。测试的是数据库结算层，没有伪造真实付款或调用支付平台通知。 |
| 数据清理与对账 | OAuth/支付临时账户、令牌、会话、授权码、订单及充值日志已清理。与本轮测试前快照相比，核心表无差异，用户/令牌额度及日志汇总不变。 |
| Linux 构建 | Linux ARM64 静态 ELF 构建通过；Dockerfile 固定摘要的三个基础镜像内容经本机代理导入后，保持其余 Dockerfile 指令不变完成 ARM64 多阶段构建。候选镜像为 Linux/arm64，Go 1.26.1、前端构建和 Debian 运行层均由 Dockerfile 执行。Docker daemon 直接访问 registry token 超时，因此没有完成“原样 FROM 摘要在线解析”，但实际使用的基础镜像内容与三个固定摘要一致。 |
| 生产模式与代理 | 在独立 MySQL/Redis、内部网络和 `LOCAL_VERIFICATION_MODE=false` 下启动，数据库迁移、系统实例、凭据刷新、订阅重置和 system task runner 正常；周期 model_update 成功，双实例运行时按周期只生成一次任务并由不同 runner 轮流领取。Nginx `proxy_buffering off` 下非流式、流式首数据及客户端取消正常，响应头前取消无消费日志。安全 Cookie 缺少可信 URL 时会 fail-fast；补齐可信 URL/代理后告警消失。 |
| 并发账本 | 本机模拟上游执行 100 请求、并发 10，100% HTTP 200；观测 74.04 req/s，P50 33.82ms、P95 330.59ms。新增 100 条消费日志，用户和令牌账本一致。该结果仅验证小规模容器/代理/账本链路，不是正式容量基线。 |
| 运行健康 | 完整候选镜像首页、status、pricing、静态前端和转发请求均 200，重启后恢复正常；独立测试容器、数据库、Redis、网络和临时凭据已清理，本地 3010 仍为 parity10/status 200。正式环境仅只读检查，未修改或重启。 |

首次对照导入时备份发现 users/user_sessions 差异，逐列比对确认只有一个用户的 `last_login_at` 更新和一个新增登录会话；额度、消费及其他核心数据未变。后续测试以本轮测试前快照为基准，清理后无新增差异，没有回退用户正常登录数据。

此前已完成同版本的三种数据库迁移矩阵、文本失败重试只结算一次、取消退款、模拟图片归档与账本一致性验证。本轮保留这些历史证据，不将其描述为真实上游联调结果。

## 上线前必须补齐

1. **冻结并审阅源码**：当前工作区仍有 127 项修改/未跟踪内容，候选镜像来自未提交工作树。正式发布前必须形成可审阅提交或明确源码快照，不能只依赖镜像标签追溯。
2. **真实外部链路**：选定渠道验证文本流式与取消、视频提交/轮询/失败退款、AistarsLab、图片实际像素；验证实际 Canvas/Video 客户端浏览器回跳和支付平台通知。当前本机模拟与本地 OAuth 成功不能证明这些外部链路已通过。
3. **正式配置与授权**：正式 compose 已具备安全 Cookie/可信代理，OpenResty 缓冲关闭且超时覆盖 180 秒；但正式应用缺少 `TEXT_RELAY_TIMEOUT=180`、`TEXT_ADAPTIVE_ROUTING_ENABLED=true`、`TEXT_SLOW_FIRST_CONTENT_SECONDS=15`，切换时必须加入。发布前准备当时的一致数据库/配置备份并取得明确授权。

多实例 system task 的数据库租约已在双容器中验证，但渠道容量、claim、重试预算、EWMA等仍不是跨节点原子协调，因此本轮不作多实例流量放行。正式发布计划与回滚步骤见私有发布目录。

## 验收工具问题

- OAuth 临时夹具首次仅初始化主库、未初始化日志库，退出调用 `CloseDB` 时触发 nil panic。补齐夹具初始化、清理首个临时账户后重跑通过；应用进程未异常。
- 系统 Go 1.24.0 启动器在带 `GOEXPERIMENT` 自动切换工具链时触发 FIPS 初始化 panic。直接使用已安装 Go 1.26.5 并设置 `GOTOOLCHAIN=local` 后编译通过；没有更改全局 Go 配置。

关键证据：首轮证据位于 `~/.local/share/new-api-github-main/readiness-20260919/`；生产模式/Linux/代理证据位于 `~/.local/share/new-api-github-main/readiness-linux-20260919/`。真实上游隔离验收位于 `~/.local/share/new-api-github-main/real-upstream-20260920/`。候选发布包、SHA256、manifest 和未执行的发布/回滚计划位于 `~/.local/share/new-api-github-main/release-20260919-parity10/`。

## 真实上游隔离续验（2026-09-20）

- 仅本地副本创建专用用户/令牌/分组/复制渠道，临时放开 sandbox 并使用本机代理；正式环境未修改。
- `gpt-5.5` 非流式与流式真实请求均 200，分别产生一条消费记录；流式 6 个事件并正常 DONE，有效首字记录 1202ms，用户/token账本一致。
- `gpt-image-2` 真实生成、单次结算和归档成功，但请求 1024×1024，实际 PNG 为 1254×1254。来源渠道 152 虽声明 1k/2k/4k，本次不能证明精确 1K，2K/4K仍未验证，不能仅凭声明放行。
- AistarsLab `/v1/models` 鉴权成功；来源渠道 17 的同步 dry-run解析 32 个模型，检测 removed=1、表达式变化33、映射变化0。未 apply、未提交付费视频，避免共享余额影响。
- 清理后临时实体和媒体文件无残留，核心表哈希、用户/token额度及日志汇总均恢复一致；sandbox 恢复 deny，代理变量清空，3010为200，正式容器启动时间未变且healthy。
- 仍需：审阅图片像素能力与AistarsLab价格变化、真实支付通知、真实客户端OAuth回跳、视频完整提交/轮询、源码冻结和上线授权。

## 2026-09-23 正式版渠道熔断与切换只读复核

- 复核对象为正式运行 digest `sha256:68f712d0e6856d7394719a141be66b62ec520f309c10e490b3bd75c23074e503`、revision `672f3da30286d790fad4a8c8ed5c603ffadfe888`；容器 healthy、restart 0。本次仅检查源码、容器环境和 options，未修改或重启正式服务。
- 正式显式配置 `RetryTimes=3`，即普通中继最多初始1次加3次重试；图片最多2次 fallback，任务提交另限最多1次。正式使用内存渠道缓存；自动重试状态码、自动禁用和亲和开关未存覆盖值，沿用该revision默认值。
- 选渠先处理Codex/Claude会话亲和，再按当前最高优先级、同优先级权重随机；重试通过请求内渠道/多Key排除集合选择最高优先级的未尝试候选。指定渠道不切换，已向客户端输出响应后不切换，视频/MJ/Suno/Jimeng等非幂等路径不自动切换；图片仅在明确安全错误下切换。
- 熔断按“渠道配置指纹 + 模型 + 请求路径”维护路由状态，并有渠道聚合和多Key状态。普通错误先累计为suspect并由15秒周期主动探测确认，suspect期间仍可接流量；确认后open。标准open/探测退避2分钟，图片从5分钟指数退避至1小时；显式网关凭据错误会把对应Key隔离10分钟。
- 恢复时标准路由从并发容量1开始，成功后逐步放大至熔断前目标；图片探测成功后直接恢复原容量。suspect/open/probing/recovery_pending持久化到 `channel_health_states` 并在启动时恢复，普通滚动统计和容量仍是单进程内存状态。
- 正式Codex/Claude规则仍为旧字段 `skip_retry_on_failure=true`，但revision 672的主relay链路没有调用该判断；实际可重试故障仍会清除当前亲和并切换，不能把旧字段解释成严格不切换。这也是候选发布时迁移为显式 `prefer` 而非 `strict` 的依据。
- 正式版尚无候选版新增的首响应/总时限、最少剩余重试时间、首个fallback独立储备、不同hostname备用优先及并发亲和CAS增强；因此本次说明不能用候选行为反推正式行为。

## 2026-09-23 正式站与测试站 Token 渠道切换、熔断、恢复对照

- 正式站运行 revision `672f3da30286d790fad4a8c8ed5c603ffadfe888`、digest `sha256:68f712d0e6856d7394719a141be66b62ec520f309c10e490b3bd75c23074e503`，`RetryTimes=3`；测试站最终候选运行 revision `33b297783252056ea133ad31b562e0d83a360d38`、digest `sha256:e3fb524fb6ab98f02780917a41ccb32fbe1be563394fdb150f522873a9740238`，`RetryTimes=5`。理论重试次数不等于实际切换次数，仍受错误分类、响应状态、候选数量、容量、总时限和预算约束。
- 两站基础选渠都按分组/模型/路径/能力/健康/容量、优先级、同优先级权重执行。正式仅有请求级渠道/Key排除和基础亲和；测试候选额外有首次fallback独立预算、不同主机优先、Retry-After冷却、90秒首响应/600秒总时限、5秒最小剩余时间和恢复亲和试流量。
- 正式单次500/429/连接失败不会立即熔断；允许重试时排除当前渠道并选最高优先级未尝试候选。指定渠道、已实际输出、非幂等任务和不安全图片错误不自动切换；正式图片最多2次安全fallback。正式未解析Retry-After，也没有测试候选的首次fallback独立预算或不同hostname优先。
- 正式路由健康按渠道配置指纹+模型+路径维护，叠加渠道聚合和多Key状态。持续异常先进入 `suspect`，由约15秒系统任务主动探测；确定性探测失败才进入 `open`。普通Token路由熔断约2分钟，图片探测按5/10/20/40/60分钟退避，上限1小时，多Key明确凭据错误约隔离10分钟。
- 测试候选安全文本路径在连续至少3次基础设施失败、相邻失败不超过30秒时提前进入 `suspect`，同时保留30秒至少20样本、失败率90%、失败跨度10秒及至少5次失败且连续2分钟无成功的标准/慢故障门。suspect期间每5秒最多放行一个真实试流量，探测在途时暂停新试流量，真实成功可以使迟到探测失效。
- 测试确认探测失败后进入 `open`；首次切换使用 `first_failover_reserve`，每请求最多一次且无可用健康备用时不消耗，后续切换受2分钟20%普通+5%应急共享预算和最低突发额度限制。备用优先不同Base URL主机，主机不可用时回退同主机健康候选；主机只是故障域近似。
- 测试429解析Retry-After秒数或HTTP日期，单次最多2分钟；无提示时按模型/路径2/4/8/16/32/64秒递增冷却。普通测试文本总时限默认600秒、每次首响应默认90秒，剩余时间不足5秒不再启动新尝试。短期冷却和稳定等待是进程内状态，不增加数据库字段。
- 正式恢复从容量1逐步放大到熔断前容量，但没有测试候选的每阶段至少10秒、每阶段3次成功、稳定等待30秒和约10%亲和试流量增强。测试恢复中失败会重新熔断；恢复中约每10次机会提供一次亲和试流量、每秒最多一次，试流量不改变原亲和；渐进恢复完成并稳定约30秒后，`prefer`亲和才正式抢回，`strict`不参与跨渠道试流量。图片探测/真实成功则直接恢复原容量。
- 测试站故障注入已验证：连续500切备用、探测熔断、恢复容量1→2→4→8→16、恢复中再次失败保留亲和、Retry-After=8冷却、约90秒无响应切备用，报告 `/opt/docker/new-api-rc20-test/verification/token-failover-20260922-r11/report.json` 为 `passed=true`。该验证使用隔离测试站和模拟上游，不等同于真实供应商长期负载验证。
- 边界：请求内切换、跨请求熔断、会话亲和和数据库自动禁用是四套不同机制；测试站逻辑、亲和迁移、图片价格、视频插件和配置不能在没有正式发布授权的情况下同步到正式站。

## 2026-09-23 测试候选补齐正式版流式输出跟踪（移植第1项，未提交未部署）

- 背景：核对发现测试候选的 `service/` 治理内核是正式 672 的超集，但 8 项 relay 集成层保护未随 parity 移植一起带过来（`maxImageFallbacks`、`relayRetriesRemaining`、`shouldEnforceChannelRetryBudget`、`uncertainRetryUsed`、`taskRetryCount`、`shouldExcludeChannelForRetry`、`hydrateInitialChannel`、`ContextKeyStreamActualOutputStarted` 全部 0 命中）。本次先做风险最高的第 1 项：流式输出跟踪。
- 问题：测试候选的 `channelResponseStarted()` 只有 `c.Writer.Written()`；gin 的 `Flush()` 会经 `WriteHeaderNow()` 置位 `Written`，而 `ResponseChunkData` 直接 `c.Render` 写出。结果只要 `response.created` 元数据发出过，任何后续失败都被标 `:response_started` 不再切换，把可恢复故障变成不可恢复。
- 实现：`constant/context_key.go` 新增 `stream_response_tracking`、`stream_downstream_started`、`stream_actual_output_started`；`service/channel_failure.go` 的 `channelResponseStarted()` 在跟踪生效时改读 `stream_actual_output_started`；`relay/helper/common.go` 的 `ResponseChunkData` 改为真实写入并置位标记，新增 `responsesEventHasActualOutput`、`MarkActualStreamOutput`、`IsResponsesTerminalEvent`；`OaiResponsesStreamHandler` 缓存 `response.created/in_progress/queued` 直到真实内容到达再 flush，并在无内容时返回可重试错误；`RelayInfo` 新增 `StreamTerminalEvent`、`StreamUsagePresent`、`StreamDownstreamStarted` 并在 `InitChannelMeta` 重置；chat/Claude/Gemini 转换路径同样开启跟踪并调用 `MarkActualStreamOutput`；`controller/relay.go` deferred 错误路径加入已开始则不追加 JSON 错误体的保护；`service/log_info_generate.go` 补充 `terminal_event`、`usage_present`、`downstream_started`、`sent_event_count` 诊断字段。
- 关键修正：缓存元数据时必须仍调用 `accumulator.Observe`，否则 `ObserveResponseModel` 与流终态观测丢失（已由 `TestResponseModelHandlersCaptureBeforeConversion` 回归捕获并修复）。
- 验证：新增三个测试文件（`relay/helper/stream_tracking_test.go`、`relay/channel/openai/stream_tracking_test.go`、`service/channel_failure_stream_test.go`）覆盖元数据不置位、内容置位、缓存顺序、无内容可重试、有内容不重试、terminal 事件无 `[DONE]` 仍正常。全仓库 `go test ./... -count=1` 44 包全通过；目标包定向 `-race`、`go vet`、`git diff --check` 通过。`controller` 偶发的 `TestSecurityAccountDeletionConcurrentRequestsHaveOneWinner` 失败已在未改动代码上复现，属既有 SQLite 并发波动。
- 状态：仅本地工作树，未提交、未推送、未构建镜像、未部署；测试站和正式站容器、数据库、Redis 均未修改。剩余 6 项集成层保护（图片 fallback 上限与预算豁免、`uncertainRetryUsed`、任务重试上限、`hydrateInitialChannel`、选择性排除、路由尝试记录）待后续逐项补齐。

## 2026-09-23 测试候选补齐全部 relay 集成层保护（本地未提交）

- 背景：在完成流式输出跟踪（第1项）后，继续补齐剩余 6 项正式 672 的 relay 集成层保护。目标是保留测试候选现有正向增强（首次fallback独立预算、90/600秒时限、429冷却、渐进恢复、亲和canary、Redis CAS）的同时，恢复正式版的非幂等与预算边界。
- 第2项 图片上限：`controller/relay.go` 新增 `maxImageFallbacks = 2` 与 `relayRetriesRemaining()`；图片路径可用重试数被封顶为 2（与 `RetryTimes` 无关），`shouldEnforceChannelRetryBudget()` 对图片路径返回 false，使图片 fallback 不被文本共享预算阻断，且每个请求的首个 fallback 始终放行。
- 第3项 不确定单次：新增 `allowsUncertainCrossChannelRetry()`（仅 Chat/Completions/Embeddings/Moderations/Rerank/Claude/Responses，且不含 `image_generation` 工具）与请求级 `uncertainRetryUsed`；504/524/流中断最多只能跨渠道重放一次。
- 第4项 任务上限：`executeTaskSubmissionWith` 新增 `taskRetryCount`，任务提交最多重试 1 次；接入 `shouldEnforceChannelRetryBudget` 与 `AllowChannelRetryFor`。
- 第5项 渠道补全：`getChannel` 在 `ChannelMeta` 为空时先按 `channel_id` 从缓存补全真实渠道对象，使排除与尝试记录获得完整 `ChannelInfo`（多Key、AutoBan）。
- 第6项 选择性排除：新增 `shouldExcludeChannelForRetry()`，只对 transient/uncertain/rate_limited/key_capability/pool_account 排除渠道；`terminal` 与 `channel_fatal` 不再被无条件排除。
- 第7项 路由尝试记录：新增 `service/channel_attempt.go`（`BeginChannelRouteAttempt`/`FinishChannelRouteAttempt`/`FinishSuccessfulChannelRouteAttempt`/`GetChannelRouteAttempts`/`AppendChannelRouteAttemptsAdminInfo`），上限 8 条；主 relay 循环与任务提交循环均接入，`service/log_info_generate.go` 以 admin-only 写入 `route_attempts`（仅渠道ID/状态码/耗时/失败分类/重试决定，不含 Key 或请求体）。
- 关键修正：首次实现时误把 `relayRetriesRemaining` 当作循环条件，导致 `RetryTimes=0` 时第一轮即退出；已改为仅作为决策函数的“剩余重试数”输入，循环仍由 `RetryTimes` 控制。该回归由既有 `TestResponsesInterruptedStreamHealth` 捕获。
- 验证：新增 4 个测试文件（`controller/relay_retry_policy_test.go`、`controller/relay_task_retry_cap_test.go`、`service/channel_attempt_test.go`，加上此前的流式测试）。全仓库 `go test ./... -count=1` 44 包全通过；`go vet ./...`、`gofmt -l`、`git diff --check` 均干净；新增代码定向 `-race` 通过。`controller` 偶发的 `TestSecurityAccountDeletionConcurrentRequestsHaveOneWinner` 失败已在未改动代码上复现，属既有 SQLite 并发波动。
- 状态：仅本地工作树，未提交、未推送、未构建镜像、未部署；测试站与正式站容器、数据库、Redis 均未修改。下一步：确定 `RetryTimes` 使用 3 还是 5，然后在测试站重跑故障注入（重点验证图片止于 2 次、任务止于 1 次、504 只切一次、流式元数据失败仍能切换），通过后再谈正式替换。
