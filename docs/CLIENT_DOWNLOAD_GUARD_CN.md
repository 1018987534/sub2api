# 安装包下载保护

本站安装包须登录后从小白教程点击下载，原始直链与内部静态目录不能公开下载。官方下载入口始终保留。

默认策略集中在 `clientdownload.DefaultPolicy`，按北京时间自然日/月计算：

| 范围 | 下载次数 | 安装包预算 |
|---|---:|---:|
| 单账号每天 | 3 次 | 2 GiB |
| 单来源 IP 每天 | 10 次 | 5 GiB |
| 全站每天 | — | 10 GiB |
| 全站每月 | — | 100 GiB |

账号与全站预算在签发时按完整文件预留，不因取消、分段、过期、未使用而退回；避免无限重复/断点请求绕过保护。IP 预算在真实传输入口按 TCP 来源计算，IPv6 同 /64 共用额度。配额持久化在 PostgreSQL，重启不清零，事务锁保证并发不超额。额度用尽返回 429，数据库或目录异常返回 503，均不放行静态文件。

凭证有效 2 分钟、仅可消费一次；同账号新凭证替换旧凭证。凭证只存在 Secure、HttpOnly、SameSite=Strict Cookie，不写 URL、页面或访问日志。下载使用独立域名，保持大文件与单段 Range 支持。中断后重新点击下载，仍按完整文件计额度。

Nginx 限制：每 IP 每秒 1 请求、突发 3；全站每秒 20、突发 10。每 IP 最多 2 条下载连接，全站最多 4 条；每连接持续限速 2 MiB/s；最多一个 Range。内部目录必须 `internal`，禁止目录列表与符号链接。

部署时先让控制端自动应用迁移 251，再安装 `nginx-download-limits.conf` 到 HTTP 的 conf.d、`nginx-downloads.conf` 到主站下载 snippet、`nginx-downloads-protected.conf` 到独立下载域名使用的 static snippet。保留部署前配置备份，`nginx -t` 通过后 reload。独立下载域名仅代理 `/downloads/clients/fetch`，固定 Host，并覆盖来源 IP 和转发头；其余应用 API 不对该域名开放。公开下载入口不再缓存，历史直链返回 403。
