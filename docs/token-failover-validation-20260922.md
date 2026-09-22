# Token 切换与恢复测试站验收（2026-09-22）

范围仅限 Oracle `/opt/docker/new-api-rc20-test`、测试库 `new_api_rc20_test` 和 `new-api-rc20-test-redis`。正式容器 `new-api-docker`、正式数据库、Redis、代理配置未修改。所有部署镜像均由 GitHub Actions 构建，以固定 OCI digest 拉取，并检查镜像 revision 标签。

## 实现与回归修复

- `e4f0b5aa6`：健康管理API和面板，嫌疑阶段限流、首次备用独立预算、429冷却、恢复试流量与亲和抢回分离、文本时限及Gemini原生文本识别。
- `afdf62a94`：释放文本总时限时，正常完成请求恢复父上下文，保留真实超时/取消状态。
- `955a4b745`：完整成功的响应即使遇到客户端随后关闭连接，仍允许记录亲和；Controller先记录完整成功结果再处理健康统计。
- `cbc50030e`：底层健康统计同样接受明确完整成功的结果，避免客户端读完响应关闭连接后，渐进恢复一直停在容量1。提前取消、未完整成功的请求不计入恢复。

默认 `TEXT_RELAY_TIMEOUT=600` 秒，`TEXT_FIRST_RESPONSE_TIMEOUT=90` 秒，显式0可禁用。首响应时限按响应体首字节计算，心跳也会结束计时；长推理需结合负载调整。故障域按Base URL主机近似判断。

## 验证方法

真实测试站HTTP请求经网关路由到仅绑定Docker网桥地址的临时Python模拟上游。创建唯一模型别名及双渠道，主渠道优先级100、备用10，使用测试库现有 `ChatGPT` 分组。整个恢复演练使用同一个 `prompt_cache_key`，从Redis确认长期亲和值；模拟500、429/Retry-After、95秒无响应及恢复中再次失败。

管理健康接口每4秒最多读取一次；429退避，防止演练本身触发管理限流。清理通过管理API删除渠道并刷新缓存，删除夹具用户、Token、日志、健康记录和亲和缓存，恢复定价别名。后台探针产生的夹具渠道日志也删除。

早期夹具问题包括使用已停用的 `default`/`GPT` 分组、错误猜测亲和缓存键含模型字段，以及管理接口轮询过密。上述问题已修正。一次部署因预期旧digest不匹配而在前置校验退出，后续演练误用前一镜像；现部署要求显式传入实际旧digest，成功后核对运行镜像与源码revision才开始演练。

## 最终结果

- 源码：`cbc50030edaca768d1cd386c058cda90cbc8d2d3`。
- Actions：[35693787381](https://github.com/CarminBack/new-api/actions/runs/35693787381)，成功。
- 测试镜像：`ghcr.io/carminback/new-api@sha256:50f30f221d64115ddc1e8af52848fef4afa8838afc95eb494f08c9de01002cec`。
- 部署前备份：`/opt/docker/new-api-rc20-test/backups/token-failover-20260922-gpFwIQ3J`，40张表与dump中的CREATE TABLE数量一致，gzip及SHA256校验通过。
- 最初稳定版备份：`/opt/docker/new-api-rc20-test/backups/token-failover-20260922-w9MtaoQq`，compose对应 `sha256:d72bbc21990adbd36899f2a3bb3118ffc17e81b518d5daf9cd5b5a730d7d92ec`；必要时仅恢复测试compose并 `docker compose up -d --no-deps new-api-test`。
- 根模块全量测试、service文本定向Race、相关vet/build及diff检查通过；渠道前端25文件281测试通过。期间SQLite并发删除测试曾波动，最终全量重跑通过。

最终故障注入报告：`/opt/docker/new-api-rc20-test/verification/token-failover-20260922-r11/report.json`，`passed=true`。

| 场景 | 实测结果 |
| --- | --- |
| 已有亲和的主渠道连续500 | 3次请求均切备用，耗时12/11/23毫秒；探测确认熔断 |
| 恢复中再次失败 | 原备用亲和保留，重新安排验证探测 |
| 全部请求使用原亲和会话 | 提供恢复试流量；容量1→2→4→8→16；稳定等待后才抢回，包含再次失败及重探共140.2秒 |
| 429 `Retry-After: 8` | 冷却期间4次请求均走备用，无再次访问主渠道；期满恢复主渠道 |
| 主渠道95秒不返回响应 | 90.03秒切至备用并成功，原用户请求未被尝试级取消中断 |
| 普通API Token读取管理健康接口 | HTTP 401 |

最终清理核对：夹具渠道/用户/Token/定价别名均为0，夹具健康记录及日志删除；模拟进程已退出，38991无监听；亲和及用量统计缓存按唯一夹具键清理。旧有2条孤儿健康记录数量不变，未在本次处理。管理操作审计保留。

最终测试站healthy、restart=0、公网HTTP 200。正式站镜像仍为 `sha256:68f712d0e6856d7394719a141be66b62ec520f309c10e490b3bd75c23074e503`，启动时间仍为 `2026-09-15T03:24:47.586858135Z`，healthy、restart=0。

此次验证不替代SQLite/PostgreSQL完整迁移矩阵、付费上游负载、支付/视频和浏览器验收。正式站上线仍需另行明确授权。
