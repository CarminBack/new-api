# 渠道17利润价格同步修复 — 2026-10-09

状态：本地修复及三数据库验证完成，待生产发布确认。

## 问题

应用同步使用`billing_expr`、`billing_mode`保存计费配置，真正运行时读取的是`billing_setting.billing_expr`、`billing_setting.billing_mode`。因此价格、利润率和模型映射可以更新，但有效计费公式和模式不更新；界面显示成功不能证明实际扣费公式生效。

生产只读核查：渠道17保存加价率35%、19个有效模型（17个按秒、2个按次），当前有效价格和公式恰好一致。另有13个上游已删除模型仍在有效计费配置中，最近多次应用成功后仍反复出现在预览删除列表。定时自动同步关闭。

## 修复

仅修改`service/aistarslab_config_sync.go`保存配置时的两处键，加上`billing_setting.`命名空间。继续使用既有`UpdateOptionsBulk`事务保存和配置刷新机制，不开启自动同步、不修改加价计算或任务计费单位规则。

历史无命名空间的孤立键不再写入，也不将其作为迁移来源；现有孤立键暂不额外删除，以避免扩大数据变更范围。

## 回归与验证

扩展既有`service/aistarslab_config_sync_test.go`，覆盖：

- 应用时价格、实际计费公式和模式同时更新。
- 按秒和按次模型；加价率35%再修改为50%。
- 清理下架模型，保留无关模型的价格、公式和渠道映射。
- 从数据库通过实际配置加载器重新加载后，再次预览所有变化列表为空。
- 旧版本遗留的无效配置键不影响新保存的有效配置。
- 测试仅接受空的隔离数据库；不得复用已有业务表。

修复前SQLite回归明确失败：期望公式`u("seconds") * 0.73`，实际仍为`u("seconds") * 0.2`；新模型的有效模式仍为ratio。修复后全部通过。

实际数据库版本：SQLite 3.50.4、MySQL 8.0.43、PostgreSQL 16.15。MySQL/PostgreSQL为本机独立临时实例，仅绑定回环接口，没有连接生产数据库。

命令（两个DSN环境变量均指向隔离实例）：

```text
go test ./service -run TestAistarsLabApplyPersistsEffectivePricing -count=1 -timeout=45s -v
go test ./service ./model ./setting/billing_setting -count=1 -timeout=60s
go build -o /tmp/new-api-channel17-sync-fix .
git diff --check
```

全部通过，无schema、依赖或前端修改。

## 待确认的生产操作

- 仅更新Oracle new-api-docker镜像，预计切换10–30秒，现有连接可能中断。
- 发布后先以现有35%加价率运行dry-run；若仍为当前19个有效模型、活跃价格/映射无变化，则应用同步，清理13个失效模型的有效计费表达式和模式。
- 若上游在确认期间发生变化，先展示新增价格/模型差异，不把未审阅的新变价一并应用。
- 不开启定时同步，不修改有效模型加价率，不额外删除历史无效配置键。
- 备份原镜像/compose、相关计费option和渠道17非敏感映射字段。失败恢复原镜像和原option/模型映射，检查容器和公网状态。
- 验证实际配置键、原35%加价率、19个有效模型、重复预览零变化，以及本机/公网接口健康。不提交付费视频生成。
