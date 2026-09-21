# parity10 本地上线前验收（2026-09-19）

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
