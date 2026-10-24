# 小白教程首版与软件下载镜像

用户已确认采用这版简洁三步图文。页面、3 张操作示意图、站内入口、系统识别与下载分流在 `cdy/beginner-tutorial-preview` 工作副本实现，点击直接在站内打开 `/beginner`。后续菜单调整已隐藏登录后系统侧栏与顶栏的“小白教程”，保留首页、API 密钥页和直达链接。服务器同步脚本和 systemd 配置一并交付，生产发布与 DMIT 定时任务安装按下文单独执行。

## 页面入口和内容

- `/beginner`（别名 `/docs/beginner`）：无需登录可阅读；已登录时使用现有站内布局。
- 首页（简洁版与默认版）右侧和 API 密钥页保留“小白教程”入口；同一标签页打开站内页面，不依赖本机 HTML 文件。登录后系统共用顶栏、用户侧栏和管理员“我的账户”菜单不显示此入口。
- 按用户反馈简化为三步：下载软件 → 在本站一键导入 → 启用配置并开始使用，第三步末尾核对使用记录。没有大主图、介绍区、侧边目录或演讲式结束语。
- 使用现有 `KeysView.importToCcswitch` / `executeCcsImport` 和 `buildCcSwitchImportDeeplink`。教程不复制、读取或在 URL 中展示用户的密钥。
- 3 张 SVG 是按实际操作流程绘制的示意图，清楚标注“操作示意”，密钥、余额等使用隐藏值。它们不是通过真实账号完成调用的截图。
- 图片可放大；手机可看，电脑下载安装；下载系统可手动修改。

## 2026-10-24 官方资料核对

CC Switch 最新稳定版 API 返回 `v4.0.19`（2026-10-23 发布）。Mac 官方安装版为 Universal DMG，支持 Apple 芯片与 Intel；Windows 为 x64 MSI 和 ARM64 MSI。最低要求以官方发布页为准，README 当前标注 Mac 12+、Windows 10+。

OpenAI 新版 ChatGPT 桌面应用包含 Codex。普通 ChatGPT 聊天区的账号、订阅和本站 API 用量单独管理，教程引导进入 Codex 使用配置。官方说明确认桌面 Codex 会读取配置目录中的供应商配置，修改后需要重启应用。没有用真实用户密钥进行付费请求，也没有验证全部 Windows 安装/登录场景。

Windows 推荐微软商店；下载到的 EXE 可能只是联网安装器。官方同时提供完整的 x64 / ARM64 MSIX，可保存到 DMIT 原样提供下载；不能把商店 EXE 当作离线包。Mac 当前入口为 Apple 芯片桌面版 DMG，Intel 用户有 CLI 备用说明。保留原厂文件，不重打包、不改签名、不代用户执行安装。

官方来源：

- https://github.com/farion1231/cc-switch
- https://api.github.com/repos/farion1231/cc-switch/releases/latest
- https://chatgpt.com/download/
- https://learn.chatgpt.com/docs/enterprise/connect-to-a-gateway
- https://learn.chatgpt.com/docs/app/windows
- https://learn.chatgpt.com/docs/enterprise/windows-deployment

## 镜像方案

`deploy/client-downloads/sync_client_downloads.py` 只依赖 Python 3.9+ 标准库。默认同步 6 个文件：CC Switch Mac、Windows x64、Windows ARM64，以及 ChatGPT Mac、Windows x64、Windows ARM64。`--ccswitch-only` 仅下载前 3 个。

脚本通过官方 HTTPS 与 GitHub Releases API 查找最新稳定版本。CC Switch 必须匹配 GitHub 返回的 SHA-256；ChatGPT 原厂目前没有在这些入口提供独立校验清单，因此只检查官方 HTTPS、HEAD 大小/ETag 和包格式，记录本地 SHA-256。MSIX 的签名结构存在检查不等于密码学签名校验，最终安装由系统校验原厂签名。

所有文件先下载到临时目录，全部通过检查才原子发布新的 manifest。失败保留上一份 manifest；文件锁阻止重入；没有更新就复用校验过的旧文件；保留最近 3 份发布目录。Nginx 仅放行 manifest 和指定安装包扩展名，不开放目录索引、锁文件、脚本或临时文件。

```sh
python3 deploy/client-downloads/sync_client_downloads.py --plan
python3 deploy/client-downloads/sync_client_downloads.py --ccswitch-only --root /tmp/client-mirror-test
```

本轮本机实际下载并验证了 3 个 CC Switch 原厂安装包。ChatGPT 的 3 个完整大文件只完成官方地址与 HEAD 大小检查，未在本轮完整下载。实时计划总量约 2.92 GB；三份保留版本加一次临时同步建议预留至少 12 GB。DMIT 只读核对有约 23.8 GB 可用空间，Python 3.13.5、systemd 257、Nginx 均存在；上线前仍需再次确认磁盘与流量预算。

## 审阅通过后的 DMIT 安装步骤

此部分是待执行部署说明。本轮没有执行以下生产写操作。

1. 按项目正常发布流程提交、合并并推送代码；获得明确上线指令后发布包含教程的 control 前端。
2. 在 DMIT 创建专用低权限用户 `sub2api-mirror`，以 root 管理 `/opt/sub2api-client-mirror`；脚本和 systemd 文件由 root 持有、普通用户不可写。创建 `/srv/sub2api-downloads/clients`，属主为该用户、权限 0755。
3. 把同步脚本安装到 `/opt/sub2api-client-mirror/sync_client_downloads.py`，将本目录的 `.service` 和 `.timer` 安装到 `/etc/systemd/system/`。
4. 为 Nginx 创建 `/srv/sub2api-public/downloads/clients`，链接到专用公开安装包目录 `/srv/sub2api-downloads/clients`；只在 DMIT 的 `xiaohondou.com` TLS server 中 include 本目录 `nginx-downloads.conf`。先备份 vhost，`nginx -t` 通过后 reload。
5. 先运行 `systemctl start sub2api-client-mirror.service`，确认全部包下载完毕、manifest 中链接和哈希正确。此阶段旧下载链接仍可用。
6. `systemctl enable --now sub2api-client-mirror.timer`。timer 在开机约 3–8 分钟执行，之后每 7 天执行一次；重启后会再次检查。用 `systemctl list-timers sub2api-client-mirror.timer` 和 journal 回读确认。
7. 从公网读取 manifest，检查每个安装包的 HEAD、Content-Length、下载类型、Range 下载和哈希。确认 Cloudflare 将 `/downloads/clients/` 仍送到 DMIT，并检查大文件响应能力；必要时再设计专用下载域名。实际安装分别在 Mac 与 Windows 人工验证。

未部署镜像时按钮安全回退到官方渠道；只有 manifest 中确实存在且路径合法的文件才显示“本站安装包”。CC Switch 缺包时回到 Releases 选正确系统，不猜版本号，也不把 ZIP 绿色版当安装版。

## 回退与验证

停止更新：`systemctl disable --now sub2api-client-mirror.timer`，保留已发布安装包。将保留的 `releases/<release-id>/manifest.json` 复制为临时文件，再原子替换根目录 manifest，可回到此前文件列表。应用界面回退使用项目已有版本发布回退流程。

本轮提供独立 `DIFF_FILE.patch`、`VERIFICATION.txt`、`ROLLBACK.sh` 及基线。回滚脚本仅恢复这一变更的文件，并要求目标副本匹配已记录的修改后哈希；在独立副本中演练后验证原始哈希一致。原项目 checkout 保持原始字节。
