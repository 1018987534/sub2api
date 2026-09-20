# 五节点调度快照 Redis 写放大修复

故障链为：每次计费探测落观测快照都会投递 `account_changed`，事件按账号关联分组重建桶；多个节点读取同一旧 watermark 重复消费，又叠加各自每 300 秒的全量重建。此前采样提供的 Redis 峰值 43.93–57.25 MB/s、约 2601–3399 SET/秒是历史证据，发布后须另测。

## 修改设计

1. `account_repo.go` 的探测更新通过 PostgreSQL `FOR UPDATE` CTE 读取当前倍率，再通过 `UPDATE … RETURNING` 比较实际保存后的数值。比较包含数据库 `decimal(10,4)` 的实际舍入结果，不依赖请求指针是否为空或旧账号对象。仅倍率真实变化时在同一事务写 `account_changed`；观测快照照常更新，提交后只刷新一次单账号缓存。身份、代理、同步开关、转换倍率和旧快照的 CAS 条件全部保留。outbox 失败仍回滚账号更新。
2. `pollOutbox` 的锁覆盖读取 watermark、取事件、处理整批事件、推进 watermark。Redis LeaderLockCache 使用每次独立 owner 和 3 分钟 TTL，处理上下文总预算 2 分钟。生产同时持有 PostgreSQL advisory lock，使 Redis 故障节点的降级路径与正常 Redis 节点仍共享互斥。Redis 失败且无 PostgreSQL 时跳过本轮；未取得锁不消费。处理或 watermark 写入失败不确认该批次。成功后先释放消费锁，再清理历史 outbox 和执行积压恢复。Redis compare-delete 防止旧 owner 删除新锁；PostgreSQL 解锁失败会丢弃物理连接，避免带锁连接回池。
3. 仅周期 `interval` 全量重建使用共享成功时间 `sched:full-rebuild:completed-at`。持全局周期锁后读取成功时间，距离上次成功不足配置 interval 则跳过；重建及共享时间写入成功才确认本轮完成。采用成功完成时间计算滚动间隔，避免节点启动相位及固定时间桶边界引起重复。失败不更新成功时间，后续节点可重试。手动、outbox、startup 和积压故障恢复仍保持原有必要重建语义。

没有数据库结构迁移，不改变节点分流目标权重、计费倍率算法、Redis 持久化配置或账号调度选择策略。`wire.go` 及生成装配向五节点调度服务注入现有 Redis lock 和 PostgreSQL，保留原插件账号目录装配。

## 验证

测试覆盖观测写入无 outbox、倍率相同无 outbox、真实变化事务提交及 outbox 失败回滚、真实 PostgreSQL CAS/数值比较、双服务并发单次消费、失败下一节点重试、Redis/PostgreSQL 同时互斥和降级、owner/过期释放、多节点启动相位、成功 cadence 去重、失败重试及手动/outbox/startup 不受 cadence 抑制。完整后端 unit、相关 race 和前端/边缘回归随发布记录保存。

部署须从当前完整 main 提交走五节点串行发布。观察共享成功时间与每节点 `fleet periodic rebuild completed` 日志，跨越至少一个 300 秒周期验证全 fleet 仅一次周期成功；同时记录 Redis INFO 的流量与 SET 速率。有限窗口的峰值只能描述该窗口，不能证明以后所有请求负载下都不会出现流量尖峰。
