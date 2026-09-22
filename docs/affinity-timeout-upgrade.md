# 亲和并发保护、旧规则迁移与文本超时分档

## 亲和并发保护

恢复试流量继续保留原会话绑定。允许更新长期亲和时，使用Redis原子比较写入；内存回退使用互斥锁，普通写入、删除和条件更新共享此锁。

迟到的备用成功不能覆盖另一请求已写入且仍可路由的高优先级绑定。失败只删除本请求最初读到的绑定值，不能删除其他请求已切换的新绑定；真正故障的原绑定仍允许删除并切备用。缓存中的同一渠道ID发生删除再重建（ABA）不作请求代际判定，与现有渠道ID缓存结构保持一致。

## 升级前显式迁移亲和规则

正式2026-09-15镜像的主重试路径不读取旧 `skip_retry_on_failure`，候选版缺少 `session_mode` 时会将其解释为strict/prefer。不能原样迁移后假定语义不变。

工具 `local-tools/affinity-migration/migrate.py` 只处理已导出的 `channel_affinity_setting.rules` JSON数组，不连接服务或数据库。默认输出脱敏决策摘要；未逐条确认的旧规则返回退出码2，不生成可应用文件。原文件及显式模式、匹配条件、模板保留；输出使用新文件且权限0600。

```bash
# 先查看哪些旧规则尚需确定模式
python3 local-tools/affinity-migration/migrate.py /secure/affinity-before.json

# 普通可切换请求显式prefer；依赖上游会话状态的规则应单独选择strict
python3 local-tools/affinity-migration/migrate.py /secure/affinity-before.json \
  --decision 'codex cli trace=prefer' \
  --decision 'claude cli trace=prefer' \
  --output /secure/affinity-reviewed.json
```

以上仅生成方案。生产应用须包含在独立批准的正式升级中：先备份原始规则，经现有管理配置接口更新rules并确认已加载，再升级代码；不能只直接UPDATE options并假定运行缓存已同步。回滚时通过管理接口恢复原始规则及原镜像。其他未确认的自定义/有状态规则保留待审，不能批量猜测为prefer。

## 超时分档

保持默认 `TEXT_FIRST_RESPONSE_TIMEOUT=90`，支持 `TEXT_FIRST_RESPONSE_TIMEOUT_RULES` JSON数组。针对原始模型名（选渠映射之前），从前到后匹配第一条；`model_pattern`采用Go `path.Match` glob语法，`*`不跨越 `/`，例如命名空间模型使用 `vendor/*`。可用 `request_path` 精确限定接口；省略则匹配所有已获准进入文本治理的接口。此规则不会把图片、后台任务或托管工具请求纳入文本治理。

```dotenv
TEXT_RELAY_TIMEOUT=600
TEXT_FIRST_RESPONSE_TIMEOUT=90
TEXT_FIRST_RESPONSE_TIMEOUT_RULES='[{"model_pattern":"reasoning-*","seconds":180},{"model_pattern":"gpt-*","request_path":"/v1/responses","seconds":45}]'
TEXT_RETRY_MIN_REMAINING_SECONDS=5
```

示例模型模式需要替换为本站实际模型。长推理规则放在通用规则之前。未匹配沿用90秒；`seconds=0`关闭相应尝试的首字节计时，总时限仍生效。非法JSON、glob、缺失秒数、负数或超过86400秒会使启动明确失败，不静默套用错误参数。

计时覆盖连接、等待响应头及首个响应体字节，首字节含心跳，不等于首个语义内容。之后继续受全请求总时限和已有流读取超时约束。新尝试默认至少需要剩余5秒，低于门槛会记录 `insufficient_time_remaining` 并停止重试，同时保留本次上游失败的健康统计。没有总截止时间或门槛设为0时不限制。

本轮没有增加按语义首内容计时、供应商/账号池故障域或跨渠道共享429冷却；这些需要进一步明确供应商信息和流式协议边界。

## 2026-09-22 测试站验证

- 源码 `3e86fa0c0da4558e4551925daf42d8f984ca5aea`；[Actions 35697234070](https://github.com/CarminBack/new-api/actions/runs/35697234070)构建成功。
- 测试镜像 `ghcr.io/carminback/new-api@sha256:dc015eb6d0462e3826b84f16faccce3b9f75d5538c1dee81d435d109c2b2c227`，核对ARM64及OCI revision后部署。
- 备份 `/opt/docker/new-api-rc20-test/backups/affinity-timeout-20260922-TfxJ9FXI`，包含compose/runtime.env/40表数据库，gzip与建表数校验通过。回滚时仅恢复测试配置并重建 `new-api-test`；原测试镜像digest为 `50f30f221d64115ddc1e8af52848fef4afa8838afc95eb494f08c9de01002cec`。
- 根模块 `go test -count=1 ./...`、定向Race（cachex/common/service/relay-channel）、相关vet、build及diff检查通过；Python迁移3条测试通过，Redis模拟及内存均覆盖条件写/删除、并发单胜者和过期行为。
- 真实测试站HTTP模拟上游：主故障13/13/25毫秒切备用；恢复中再失败保留亲和；完整容量进阶与稳定抢回141秒；429冷却通过；迟到备用成功不能覆盖新主亲和；临时模型先映射为其他上游模型仍在3.01秒按原始模型规则切备用；普通Token访问健康管理接口401。
- 报告 `/opt/docker/new-api-rc20-test/verification/affinity-timeout-20260922/report.json` 为 `passed=true`。临时模型3秒规则已从runtime.env及运行容器移除，测试站恢复默认首字节90秒/总时限600秒，新尝试最低剩余时间5秒。
- 用户/Token/渠道/定价/健康记录/日志/亲和及统计缓存均按夹具范围清理，模拟38991端口已无监听；既有2条孤儿健康记录未改变。测试站healthy、restart=0、公网200。
- 正式站保持原镜像 `68f712d0e6856d7394719a141be66b62ec520f309c10e490b3bd75c23074e503`、原启动时间 `2026-09-15T03:24:47.586858135Z`，healthy、restart=0。正式规则只读导出，迁移方案在本地生成，未应用到正式配置。
