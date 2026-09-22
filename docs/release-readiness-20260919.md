# new-api 上线前验收

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
