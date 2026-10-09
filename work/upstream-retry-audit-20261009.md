# New API 上游重试修复评估 — 2026-10-09

状态：本地补丁和数据核对已完成，生产未变更。

## 数据范围与口径

- 窗口：2026-09-25 04:08:59 UTC 至 2026-10-09 04:08:59 UTC，连续14天。
- 来源：Oracle MySQL 8.4.9 的 logs，按创建时间索引逐日读取，共213,036条消费/错误记录。查询仅导出模型、接口、reasoning、token数量、计时、终态、渠道和重试原因，不导出用户身份、令牌或请求正文。
- 最初的多次JSON_EXTRACT查询触发20s查询上限；改为FORCE INDEX和JSON_TABLE单次解析后，14个日片均成功，不改数据库配置。
- 下表使用可确认完成的子集：非流式消费记录；或流式status=ok、end_reason=eof且completion_delivered=true。旧日志缺乏终态字段和完成后客户端断开的记录另列，不计入该子集。这是保守统计，不代表所有成功请求。
- 首字节是上游HTTP响应首字节，可能只有响应头；首SSE是第一个SSE事件，仍不保证是模型正文。失败请求的502错误页未混入成功分位数。
- 统计使用成功最终尝试的计时；失败重试累计时长另外分析。未生成精确的策略重放成功率；不能把越过候选阈值的比例直接视为新增失败率。
- 日志按接口分类无法完整还原媒体、后台和托管工具等请求体资格；配置生效仍由现有IsTextRelayRequest判定。原生图片接口数据不用于文本参数建议。
- 日志中的prompt_tokens为上游报告的输入token数量，不能直接等同于发起请求前的token估算。

## 主要结果

| 模型 / 接口 | HTTP首字节有效样本 | HTTP P95 | HTTP P99 | SSE P95 | SSE P99 |
|---|---:|---:|---:|---:|---:|
| gpt-5.6-sol /chat/completions | 33,368 | 15.763s | 24.845s | 无 | 无 |
| gpt-5.5 /responses | 27,979 | 23.536s | 48.996s | 22.781s | 49.247s |
| gpt-5.6-sol /responses | 16,695 | 28.198s | 56.734s | 34.444s | 69.886s |
| gpt-6-sol /responses | 17,134 | 24.103s | 48.247s | 25.427s | 51.208s |
| gpt-6.1-sol /responses | 14,009 | 35.848s | 67.283s | 36.600s | 74.677s |
| gpt-6-astra /responses | 9,639 | 31.386s | 59.190s | 32.284s | 62.050s |
| gpt-5.6-terra /responses | 7,744 | 21.732s | 43.454s | 23.018s | 47.737s |

- gpt-6.1-sol /responses、max、输入>128k：649条完成记录，HTTP P95=79.477s、P99=101.025s，SSE P99=104.899s。全局45/90s会覆盖正常慢请求，不能直接部署。
- gpt-6.1-sol /responses、xhigh、32k–128k：2,408条，HTTP P99=71.261s、SSE P99=79.861s，含此前尝试后的请求至响应P99=101.690s。
- gpt-5.6-sol /chat/completions：1,696条request_context_done错误记录，其中1,689条用时四舍五入到5秒桶落在30s。说明该流量群很可能存在约30秒的客户端/下游限制，成功样本也受该限制截断；不能从成功P99=25s推导全部请求45s安全。
- gpt-5.6-sol /responses：52条request_context_done错误记录，其中29条在125s桶、6条在120s桶；这只能说明部分流量在此区间取消，不能当作所有客户端的固定上限。
- token.mewinyou.shop的OpenResty读取/发送/连接超时均为600s。未确认主要用户客户端的实际配置；Cloudflare与供应商入口是另外的超时层。

## 本地修复

基线：与生产OCI revision一致的e8912108ab9850408ac735c2c4f1ff053ce5d47a，目录new-api-unset-price。

1. 重试优先避开本请求全部已尝试的供应商域名；没有符合约束且健康的独立候选时保留同域兜底。
2. 新增TEXT_FIRST_RESPONSE_TOTAL_TIMEOUT，默认0关闭；各次首次响应体等待共用截止时间，已有正常响应流不受首响应预算截断。
3. 既有TEXT_FIRST_RESPONSE_TIMEOUT_RULES增加total_seconds与reasoning_effort匹配，支持按原始模型、接口和显式reasoning档位启用预算。保留规则顺序，匹配第一条规则；total_seconds=0可覆盖全局预算。没有声明reasoning的规则仍可作为通用规则。
4. reasoning从原始请求体读取：Responses reasoning.effort、Chat reasoning_effort、Claude output_config.effort；渠道参数覆盖不会改变原始超时分类。
5. 发起请求前或等待响应头阶段总预算耗尽，返回504/code=first_response_budget_exhausted并停止重试。剩余时间不足的停止原因记录到策略日志。响应体阶段的现有协议错误处理仍可能保留原始错误码，尚未统一所有协议的预算错误呈现。
6. 明确502原本立即进入重试，无需等到定时器到期；已单独补回归测试。

尚未实现：按估算上下文长度匹配、按流量百分比灰度、人工供应商标签、供应商级熔断。不能把次数或共享预算用尽直接称为所有上游失败。

## 上线建议

- 撤回全局45/90s方案。
- 第一阶段仅部署请求级跨供应商选路修复及可配置预算能力；总预算保持0、原单次首响应90s保持不变，不改现有渠道优先级。此阶段不能保证解决所有长时间等待，但可减少绕回已失败供应商。
- 下一阶段在确认客户端上限的受控流量上，以模型/接口/reasoning限定灰度，评估预算耗尽、完成率、重试次数和延迟；不要直接将low视为快速请求，low+较大上下文也有较长尾延迟。
- 确认客户端上限后，总预算留足网络、写回和清理余量；短客户端超时应先修正客户端或为该流量单独设置合适预算。
- 上线须单独确认：目标仅Oracle new-api-docker，预计容器切换10–30秒，已有连接可能中断；备份compose并保留旧镜像digest，失败恢复原镜像/环境变量，仅重建new-api服务。
- 验证容器健康、本机/公网status、重试链是否绕回故障host、成功交付率和取消比例。灰度阈值最终尚未选定。

## 验证

- go test ./service ./relay/channel -count=1：通过。
- go test ./controller -count=1 -timeout=120s：通过。
- 补充模型/接口/reasoning规则后：go test ./common ./service ./relay/channel ./controller -run 'Test(Text|ManagedRetry)' -count=1 -timeout=60s：全部通过。
- go build -o /tmp/new-api-upstream-fix-local .：通过。
- gofmt、git diff --check：通过。
