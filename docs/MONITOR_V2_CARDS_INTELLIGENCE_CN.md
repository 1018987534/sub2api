# 独立 V2 卡片与降智检测

## 页面边界

- /monitor 原官方页面保持不变。
- /monitor-v2 新卡片视图只读取现有 V2 matrix/snapshot；被动指标、健康评分、缓存率口径和调度器不改。
- /admin/intelligence-checks 仅管理员可读写分组探针配置、运行和历史详情。
- 普通用户只能读取自己有权访问且被 V2 配置展示的分组记录；后端不返回答案、错误正文、提示词、Key ID、操作人或配置快照。
- 公共卡片固定文案：gpt-6-astra · low · 每分钟检测 · 近 60 分钟 · 仅显示已检测记录。这是产品展示文案，不是调度时钟；管理页明确显示实际配置。每十分钟运行时一小时约六条记录，不补齐六十个绿色格子。

## 默认配置与真实执行

默认关闭，模型 gpt-6-astra、effort low、间隔 10 分钟、超时 900 秒。在降智检测管理页选择分组后，直接从「管理员 API Key」下拉框选择当前登录管理员名下、已绑定本分组的站内 API Key，不再手填 ID。这不是系统管理接口的 Admin API Key。支持搜索、刷新和分页；停用、过期或额度耗尽的 Key 显示原因且不可选。没有 Key 时先在「API 密钥」创建并绑定分组，再回来刷新。

候选接口 GET /api/v1/admin/intelligence-checks/:groupID/keys 受管理员权限和现有合规确认门禁保护，按当前登录用户和分组过滤。仅返回 ID、名称、分组、可用性、剩余额度和到期时间，不返回凭据。配置只存 api_key_id，执行时后端重新校验归属、分组、状态和额度，并取出凭据；切换管理员后不能直接保存他人的 Key。刷新、搜索或翻页不自动清空已保存的选择，最终仍由保存接口校验。模型能否使用取决于该分组实际授权；不假设它在任何上游存在。

支持 Responses、Chat Completions、Messages；Messages 不发送 reasoning effort。请求只发送到本机网关固定地址，经原有鉴权、调度、计费链路；不能填写任意远端 URL，不跟随重定向，不重放或自动重试 POST。

控制面每 10 秒扫描到期任务，以配置的 interval_minutes 为准。运行须同时满足控制面角色、V2/被动聚合运行开关与 V2 配置开启。INTELLIGENCE_CHECKS_DISABLE_RUNNER=1 停止自动扫描（手动检测仍受 V2 开关限制）。配置保存不会撤销已发出的请求；在途请求使用不可变配置快照。

默认规则参考 FP CandyAnswerPolicy candy-v3：NFKC 标准化后全文含“手感”或“21”即 normal，否则 degraded；可改关键词或 exact。该规则是启发式，不宣称数学真值、模型身份或 IQ。空、截断、超时、非成功 HTTP、无完整终止事件一律 error，不因部分输出含关键词而判正常。

## 隔离与并发

独立 internal/intelligence 包及迁移 241_intelligence_checks.sql，只增加两张表，不改官方监控表。配置版本 CAS 防覆盖；数据库 FOR UPDATE SKIP LOCKED、租约及 fencing token 防多实例/手动与定时重叠；本实例最多并发四个检测。结果写入失败会记录服务日志，不生成虚假成功记录。

公共窗口按服务端时间近 60 分钟过滤；管理历史近 7 天，24 条游标分页。小时清理按 1000 条批处理并受上下文超时限制。只保存完成时解析出的答案，响应上限 2 MiB，答案上限 64 KiB；回显专用 Key 会替换为 [REDACTED]。

## 本地验证

    cd backend
    CGO_ENABLED=0 go test ./internal/intelligence ./internal/handler ./internal/server/routes ./internal/service -run 'Test(Intelligence|ChannelMonitorV2)' -count=1
    CGO_ENABLED=0 go build ./cmd/server
    cd ../frontend
    pnpm install --frozen-lockfile
    pnpm exec vitest run src/features/channel-monitor-v2-cards/__tests__ src/views/admin/__tests__/IntelligenceView.spec.ts src/api/__tests__/channelMonitorV2.spec.ts
    pnpm build

测试使用 httptest、SQL mock、Vue 组件 mock，不调用生产或付费模型。SQL mock 不等同于真实 PostgreSQL 迁移验收；部署前仍需官方迁移流程和真实分组凭据联调。没有生产发布授权时不得执行迁移、改运行配置或发起付费探针。

## 回滚

关闭各分组自动检测并等待在途请求完成，切回之前版本代码即可；增加的表保留，不删历史。发布时按项目既有控制面优先迁移门禁；不得手工写迁移记录。交付目录中的 ROLLBACK.sh 只用于无 .git 的源码验证副本，不能替代数据库/生产回滚。
