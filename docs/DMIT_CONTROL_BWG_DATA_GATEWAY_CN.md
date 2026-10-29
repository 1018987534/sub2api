# DMIT 主控和数据层：四节点生产拓扑

## 当前状态

2026-10-29，BWG `95.169.18.157` / `bwg-us-01` 退役。本文保留原文件名供历史链接使用。
BWG 已从动态调度名单删除，同时移除 Worker 静态 origin、DNS 和 DMIT WireGuard peer。
不能只把 BWG 权重设为 0：已配置的零权重节点仍可能被紧急兜底选中。

| 实例 | 主机 | 职责 | 数据访问 | 网关 origin |
|---|---|---|---|---|
| `dmit-us-01` | `179.255.105.184` | 唯一 control、嵌入前端、PostgreSQL、Redis、Komari | 本机 `10.20.0.1` | `gateway3-origin.xiaohondou.com` |
| `vmiss-us-01` | `38.47.117.85` | gateway | WireGuard → DMIT | `gateway-origin.xiaohondou.com` |
| `yt-us-01` | `154.23.243.26` | gateway | WireGuard → DMIT | `gateway154-origin.xiaohondou.com` |
| `vmiss-us-02` | `38.47.113.166` | gateway | WireGuard → DMIT | `gateway2-origin.xiaohondou.com` |

权威 PostgreSQL 容器是 DMIT 的 `sub2api-migration-postgres`，Redis 容器是
`sub2api-migration-redis`。BWG 的旧数据容器自 2026-10-23 起已停止；其旧卷不能重新作为主库启动。
所有运行节点使用 DMIT 的 `10.20.0.1:5432` 和 `10.20.0.1:6379`，数据端口不对公网开放。

## 边缘路由

主站 `xiaohondou.com` 的网页、管理和普通 API 进入 DMIT。
`control-origin.xiaohondou.com` 指向 DMIT，提供带专用 token 的运行时配置。
Responses 的三类路径及两类 Chat Completions 路径由现有 Worker 分流。
退役的 `nideyiyi.com` 和 `api.nideyiyi.com` 应用 DNS 和 Worker 路由不得恢复。

权威目标来自 DMIT `settings.gateway_routing_settings`；
有效权重来自 `GET /api/v1/gateway-routing/runtime`。2026-10-29 摘除 BWG 后的目标为：

| 实例 | 目标权重 |
|---|---|
| `vmiss-us-01` | 22% |
| `yt-us-01` | 57% |
| `vmiss-us-02` | 7% |
| `dmit-us-01` | 14% |

以上保留原四台存活节点 15:40:5:10 的相对比例，按最大余数法分配至 100%。
后续以管理员配置为准。Worker 中仅保留这四台的静态变量，与本次目标一致。

配置运行时地址时，Worker 冷启动及缓存过期均等待权威配置，获取失败返回 503；
不使用静态或过期权重。缓存 TTL 为 1 秒。已经发出的流不能迁移，应先摘流并确认请求排空再停服务。
不要因连接失败、5xx 或模型错误重放 Responses POST。
仅 GET/HEAD 可在现有规则允许时故障切换。

现有流量阈值、健康和丢包保护保持启用。零权重备用和全节点故障时的紧急兜底仅从配置中的节点选择，
因此退役节点必须从 `nodes` 删除。历史 `usage_logs.instance_id=bwg-us-01` 继续保留。

## 发布与监控

DMIT control 使用 `-tags embed` 构建；三台 gateway 使用无前端产物。
常规应用发布仍使用兼容文件名 `release_five_node.sh`，但其目标仅包含当前四台节点。
发布前从 DMIT 的 `sub2api-migration-postgres` 检查 schema：

- 无待执行 migration：依次发布 VMISS-01、YT、VMISS-02、DMIT。
- 存在兼容的待执行 migration：先备份 DMIT PostgreSQL 并以 DMIT control 执行官方 migration，
  再发布三台 gateway。不得手工写 migration 表。

发布保留既有 main 提交、远端同步、干净构建、逐节点检查和失败回滚门禁。
数据库、Redis 和其他业务不随应用发布重建或重启。

Komari 位于 DMIT；公开页面为四节点监控。BWG 记录隐藏以保留历史指标，
首页 ping 和任务绑定去掉 BWG，不能让退役节点重新成为调度候选。

## 退役与恢复边界

退役前已确认：主站、PostgreSQL、Redis、Komari 均在 DMIT；
三台存活 gateway 通过 WireGuard 直接访问 DMIT；BWG 未运行其他独立业务。
先删调度、等待请求排空，再停 BWG 应用、Nginx、WireGuard、监控代理及相关 timer；
最后移除 DNS、Worker BWG 变量及 DMIT peer。

BWG 原始配置与旧数据归档于 DMIT 的 `/root/bwg-retirement-20261029/`。
该目录包含敏感配置和旧数据库，权限保持 root 私有，不提交仓库或公开。
备份核验与机器关机/供应商释放是不同状态；供应商账单取消须以供应商实际回执为准。

恢复现行服务应使用 DMIT 的当前数据和当前四节点备份。禁止从 BWG 陈旧 PostgreSQL 数据回滚现行业务。
确需恢复 BWG gateway 时，必须先恢复其到 DMIT 的专属 peer、配置、源站 TLS/DNS 和监控，
验证直接健康及鉴权后，再显式把节点加回管理员配置；不启动旧 control 或旧数据容器。
