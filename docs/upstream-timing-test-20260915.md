# 上游首响应分段计时：测试站验证（2026-09-15）

## 状态

- 诊断实现提交：`360e086bea3ebafafd4405956945568484571a53`。
- 测试站：`tokentest.mewinyou.shop` / oracle `new-api-rc20-test`。
- **测试已暂停，测试站已回滚到 `carmin-20260903-bbbc9b5`**。正式 `new-api-docker` 保持 `carmin-20260915-672f3da`，未重建。
- 本轮不能证明原请求上游 31.2 秒与本站 75.15 秒的具体差额原因。没有上游日志或同次调用的上游首字数据。

## 实现与计时语义

`relay/common/upstream_timing.go` 使用同步保护的 HTTP trace 记录每次 HTTP exchange，`relay/channel/api_request.go` 接入；`relay/helper/stream_scanner.go` 记录首条非空、非 DONE 的 SSE data。消费日志 `other.admin_info.upstream_timings` 保留各次 exchange 的独立快照；管理员详情显示原始诊断字段。

- `request_to_dispatch_ms`：请求级原始计时起点到本次 HTTP 发起；重试时包含之前尝试，不应全部称作本站计算开销。
- `connection_ready_ms`、`request_written_ms`、`first_response_byte_ms`、`response_headers_ms`、`first_sse_data_ms`：相对于本次 HTTP 发起的累计时间点，**不能相加**。
- `dns_ms`、`connect_ms`、`tls_ms`：对应回调阶段的耗时；连接复用时没有发生的阶段缺省，不伪造为零。多次底层拨号的值是最后记录的阶段，不等同于完整拨号事件轨迹。
- `connection_reused`：是否复用连接。
- 首个 SSE data 不保证是首个可见文字，也不是 sub2api 内部的模型 TTFT。
- 本轮未覆盖非 HTTP 适配器，也未把该快照接入全部错误日志路径。

本地验证：`go test ./service ./controller ./relay/...`、`go test -race ./relay/common`、前端 typecheck、涉及文件 oxlint、3 个组件测试及前端生产构建通过。

## 经测试站公网链路的模拟结果

| 场景 | 响应头（自发起） | 首个 SSE（自发起） | 网关首响应 | 客户端首个 SSE |
|---|---:|---:|---:|---:|
| 响应头立即返回，数据延迟 1 秒 | 1 ms | 1001 ms | 1015 ms | 1064 ms |
| 响应头延迟 4 秒，再等数据 1 秒 | 4001 ms | 5001 ms | 5013 ms | 5055 ms |
| 响应头立即返回，数据延迟 5 秒 | 0 ms | 5001 ms | 5010 ms | 5050 ms |

说明计时可以区分等待响应头与响应头之后等待流数据。这里只验证测量，不证明真实异常来自代理缓冲。

## 真实调用

临时复制生产渠道 186 的连接配置到测试库，以已定价别名 `gpt-5.4` 映射到 `gpt-5.6-sol`，调用 `/v1/responses`，只请求返回 OK，`max_output_tokens=64`。不读取原请求正文，不修改生产渠道。

第一条成功请求 `202609150418343415484928268d9d6RQKgAewM`：

- 本次请求发起前 9 ms；连接就绪 61 ms（DNS 12 ms、连接 9 ms、TLS 38 ms）。
- 首响应字节及响应头 15050 ms，首个 SSE 15060 ms。
- 网关首响应 15069 ms；客户端首个 SSE 15286 ms；总耗时约 15.6 秒。
- 本例主要时间位于发出请求后等待上游响应头；响应头到首个 SSE 仅约 10 ms。无法进一步区分上游排队、内部重试、模型生成或链路中间节点。
- 第二次真实请求遇到本轮测试配置事故而中断；第三次未发送，不能用第二次 502 推断上游性能。

## 测试配置事故与恢复

1. 首次测试临时用户使用已弃用 `default` 分组，鉴权返回 403，未调用上游；清理时字符串排序规则冲突，随后按已知临时 ID 清理成功。
2. 临时渠道使用独立分组且未创建 abilities，原意是仅通过管理员指定渠道调用、避免普通流量选中。但 `model/channel_cache.go:65` 的既有实现只按 abilities 初始化分组 map，在定时扫描启用渠道时对缺失分组写 nil map，触发 panic。
3. 测试容器在 12:16:54 和 12:18:55 CST 两次自动重启，后一次打断第二条真实请求，公网返回 OpenResty 502。该事故由本次测试夹具配置触发，不是原始首响应差异的证据。
4. 已删除临时用户 74/75、令牌 8525/8526、渠道 8545–8548；无新增 abilities。临时 HTTP 服务已退出，测试用户缓存清理，匹配令牌缓存数为零。
5. 回滚测试 Compose 到原 digest `sha256:728eee31f3043c595beaf24402b24ae58a33b690ce16828e8ea7254be0c81531`，不恢复整库，避免覆盖测试站其他正常数据及诊断日志。
6. 回滚后跨过 60 秒同步周期复查：测试容器 healthy、restart=0，公网状态 success=true、旧版本正确。正式应用和两套 Redis 未重启。

备份目录：`oracle:/opt/docker/new-api-rc20-test/backups/upstream-timing-20260915T041452Z/`，保存发布前 Compose、环境文件、inspect、测试库单事务备份。秘密仅保存在受限服务器备份，不记入本文。

## 重测结果（缓存修复后）

- 修复提交 `9125caa40cf6527241375544dba13d3ca0e812d6`：`InitChannelCache` 遇到启用渠道声明了 abilities 尚未初始化的分组时主动建立 map，并增加回归测试。相关 Go 测试通过；Actions `34929262718` 成功。
- 测试站部署 digest `sha256:51b67d938d39b26f3f2cc824fc5a383e4a06c61c110a7771fb6474f75d0c1adf`，备份 `oracle:/opt/docker/new-api-rc20-test/backups/upstream-timing-retest-20260915T043639Z/`。跨越多个 60 秒同步周期后 healthy、restart=0、无 panic；正式服务及两套 Redis 未重启。
- 模拟三场景再次通过，响应头/首 SSE 分别约 `1/1001 ms`、`4001/5001 ms`、`1/5001 ms`。
- 相同简短请求连续完成 10 次真实调用，全部 HTTP 200、单渠道、无重试。其中 8 次首 SSE 为 `0.87–2.184 秒`，2 次分别为 `45.330 秒` 和 `48.112 秒`，成功复现“部分请求差异特别大”的随机长尾。
- 两条慢请求的连接就绪仅 `41/35 ms`，本站请求前置仅 `9/8 ms`；但首响应字节/响应头为 `45.320/48.103 秒`，响应头到首 SSE 均仅约 `10/9 ms`。因此长尾发生在本站写完请求后、收到上游 HTTP 响应头前，不是本站渠道选择/计费前置、跨渠道重试或 SSE 响应头后缓冲。仅凭本站仍无法区分 sub2api 排队、其内部重试、模型后端等待，或发出请求后的中间链路等待。
- 测试夹具（用户 76、令牌 8527、渠道 8549/8550及 abilities）全部删除并核对为零；临时服务退出，匹配缓存为零。诊断版目前保留在测试站，版本 `carmin-20260915-9125caa`。

## 后续

已取得充分复现证据。建议正式站只上线分段诊断，不修改现有网关首响应口径；管理员详情并列展示响应头及成功渠道首 SSE。正式发布需再次确认并按现有 digest 备份/回滚。无法将 sub2api 面板首字直接换算为本站数值。
