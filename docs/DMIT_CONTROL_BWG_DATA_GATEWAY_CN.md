# DMIT Control + BWG Data Gateway 生产拓扑

## 目标状态

自 2026-09-09 起，Sub2API 五节点按以下职责运行：

| 实例 | 主机 | 应用角色 | 数据访问 | 对外入口 |
|---|---|---|---|---|
| `dmit-us-01` | `179.255.105.184` | 唯一 `control`，包含前端、管理、登录、计费和后台任务 | WireGuard `10.20.0.1` | `xiaohondou.com`、`control-origin.xiaohondou.com`、`gateway3-origin.xiaohondou.com` |
| `bwg-us-01` | `95.169.18.157` | `gateway`，保留 Responses 请求内调度 | 本机 Compose 网络 | `gateway-bwg-origin.xiaohondou.com` |
| `vmiss-us-01` | `38.47.117.85` | `gateway` | WireGuard `10.20.0.1` | `gateway-origin.xiaohondou.com` |
| `yt-us-01` | `154.23.243.26` | `gateway` | WireGuard `10.20.0.1` | `gateway154-origin.xiaohondou.com` |
| `vmiss-us-02` | `38.47.113.166` | `gateway` | WireGuard `10.20.0.1` | `gateway2-origin.xiaohondou.com` |

权威 PostgreSQL、Redis 和面向远端节点的 relay 保留在 BWG。BWG 应用使用
Docker 服务名直接连接本机数据层；其余四个应用节点通过 WireGuard 访问
`10.20.0.1:5432` 和 `10.20.0.1:6379`。数据库、Redis和应用端口均不暴露公网。

`INSTANCE_ROLE=control` 只能出现在 DMIT。数据库 migration、聚合、扫描、支付
过期、备份和其他 control 后台任务不得在 BWG gateway 重复运行。所有 gateway
继续执行 Responses 请求链上的鉴权、账号选择、额度、计费、用量写入和缓存失效。

## 边缘路由

- `xiaohondou.com` 的普通网页和非 Responses API 固定进入 DMIT control。
- `control-origin.xiaohondou.com` 指向 DMIT，只服务 control 运行时和运维验证。
- `gateway3-origin.xiaohondou.com` 仍指向 DMIT，供 Responses Worker 独立访问。
- `gateway-bwg-origin.xiaohondou.com` 指向 BWG，只允许 health 和三类 Responses 路径。
- Worker 继续只绑定 canonical 域名的 `/v1/responses*`、`/responses*` 和
  `/backend-api/codex/responses*`，不得恢复退役域名路由。

动态权重由 DMIT control 的 `gateway_routing_settings` 和
`GET /api/v1/gateway-routing/runtime` 提供。Worker 的静态百分比仅用于冷启动
回退。角色迁移不自动改变管理员目标权重；调整权重必须单独记录并验证五个节点之和。
迁移完成后的初始目标与冷启动回退统一为 BWG 10%、VMISS-01 10%、YT 54%、
VMISS-02 10%、DMIT 16%，后续可从管理端动态调整。

## 角色化产物

- DMIT 必须使用 `-tags embed` 构建的 control 二进制，`/admin/accounts` 返回前端。
- BWG、VMISS-01、YT和 VMISS-02 必须使用无 `embed` 的 gateway 二进制，前端路径
  返回 404，Responses 未鉴权请求返回 401。
- 五个实例必须运行同一版本和同一 commit。差异只允许来自角色、实例 ID、数据连接、
  Nginx源站和主机级配置。

## 正常发布顺序

发布前从 BWG PostgreSQL执行 schema gate。

- 无 pending migration：`bwg-us-01 -> vmiss-us-01 -> yt-us-01 -> vmiss-us-02 -> dmit-us-01`。
- 有 pending migration：先在 BWG 创建并验证 PostgreSQL备份，发布 DMIT control并确认
  migration 完成，再按上述顺序发布四个 gateway。

每次只重启一个应用节点。PostgreSQL、Redis、relay、WireGuard和无关容器不得随应用
发布重建或重启。每个节点必须完成版本、commit、二进制 hash、角色、ready、Nginx和
直接 origin 验证后才能继续下一个节点。

## 主控迁移切换

从旧拓扑切换时先把 DMIT 和 BWG 的动态权重临时设为 0，并保证 Worker 冷启动回退也
不会把 POST 送到切换中的节点。随后按以下顺序执行：

1. 准备 DMIT 的 canonical/control TLS、完整 Nginx vhost和嵌入前端的 control产物。
2. 准备 BWG 的 `gateway-bwg-origin` TLS、Responses-only Nginx vhost和 gateway产物。
3. 备份两台 `.env`、Compose、Nginx和当前二进制，记录 SHA-256。
4. 停止旧 BWG control应用；保持 PostgreSQL、Redis和 relay运行。
5. 以 `INSTANCE_ROLE=control`、`INSTANCE_ID=dmit-us-01` 启动 DMIT，验证远端数据层、
   migration、前端、登录、管理、计费和运行时权重接口。
6. 把 canonical 和 `control-origin` 切到 DMIT，验证 Cloudflare/public边界。
7. 以 `INSTANCE_ROLE=gateway`、`INSTANCE_ID=bwg-us-01` 启动 BWG，验证本机 DB/Redis、
   404 前端边界和 Responses 401/真实请求。
8. 恢复两节点目标权重，验证 Worker 运行时、五节点用量行和监控角色。

切换窗口不得同时运行两个 control，避免后台任务重复。创建 Responses 的 POST 不得因
连接失败在边缘重放。

## 回滚

角色切换失败时保持 PostgreSQL和 Redis在 BWG运行：

1. 把 BWG和 DMIT 的 Responses 权重设为 0。
2. 停止 DMIT control，恢复其 gateway `.env` 和无前端二进制。
3. 恢复 BWG control `.env` 和嵌入前端二进制并启动。
4. 把 `xiaohondou.com`、`control-origin.xiaohondou.com` 恢复指向 BWG。
5. 验证前端、管理 API、运行时权重、Responses 和共享用量后再恢复节点权重。

回滚不恢复旧数据库、不复制数据卷，也不启动双 control。数据库始终以 BWG当前
PostgreSQL为唯一权威数据源。

## 完成标准

- DMIT ready 报告 `role=control`、`instance_id=dmit-us-01`，前端和完整 API可用。
- BWG ready 报告 `role=gateway`、`instance_id=bwg-us-01`，前端路径 404。
- BWG本机 PostgreSQL、Redis及四个远端节点的 WireGuard数据路径均正常。
- Worker运行时包含五个 ID，所有正权重 origin ready，Responses POST无重放。
- Komari保留五个 UUID，DMIT 标记 `control`，其余四台标记 `gateway`。
- 发布脚本、技能、Worker fallback和本文件描述完全一致。
