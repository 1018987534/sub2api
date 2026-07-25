# Sub2API 按域名展示与分组隔离最小方案

## 1. 结论

采用“域名配置 + 分组白名单”即可满足当前目标：

- `xiaohondou.com` 作为默认 C 端主站。
- `xiaofanqie.org` 复用同一套前后端、数据库、用户、余额和上游账号池。
- `nideyiyi.com` 和 `api.nideyiyi.com` 作为老域名，以 `308` 重定向到 `xiaohondou.com`。
- 不创建品牌表、域名表、用户品牌字段、订单品牌字段或品牌成员权限表。
- 前端按域名切换 Logo、标题、副标题、客服联系方式和 API 端点地址。
- 后端只按域名限制用户可看见、可选择和可购买的分组。

这不是完整 SaaS 多租户方案，但改动小，适合先验证双品牌价格与展示效果。

## 2. 明确不做的事情

第一版刻意不做以下内容：

- 不区分用户归属品牌，也不记录注册来源域名。
- 不隔离用户余额、订单、账号池、调度资源或管理员后台。
- 不做跨域单点登录；不同顶级域名下用户仍用相同账号分别登录。
- 不改 API 网关鉴权，不限制 Key 从哪个 API 域名发起调用。
- 不做 B 端邀请、企业认证、审批或客户成员管理。
- 不复制前端项目，也不维护两套 Settings。

因此，同一个用户可以登录两个域名；如果 B 端分组公开，用户也可以通过 B 端域名使用 B 端价格。这是当前“用户不按域名做实际隔离”的直接结果。

## 3. 配置模型

新增一个系统设置 `domain_brand_config`，使用 JSON 保存 Host 到展示和分组配置的映射。

示例：

```json
{
  "domains": [
    {
      "domain": "xiaohondou.com",
      "api_base_url": "https://xiaohondou.com/v1",
      "allowed_group_ids": [5, 59, 87]
    },
    {
      "domain": "xiaofanqie.org",
      "site_name": "xiaofanqie.org",
      "site_logo": "",
      "site_subtitle": "面向企业团队的 AI 服务",
      "contact_info": "企业客服联系方式",
      "api_base_url": "https://xiaofanqie.org/v1",
      "allowed_group_ids": [101, 102]
    }
  ]
}
```

配置约束：

- Host 统一转为小写并去掉端口后匹配。
- `allowed_group_ids` 必须全部是存在且启用的分组。
- 当前未配置 Host 时，回退全局配置，保证本地环境和其他兼容入口不受影响。
- 一个分组 ID 只能归属一个已配置域名。
- `site_logo` 缺省表示继承全局 Logo，显式空字符串表示使用前端内置默认 Logo。
- `contact_info` 和 `api_base_url` 缺省表示继承全局设置，配置非空值时按 Host 覆盖。

后台“系统设置”提供域名卡片和活跃分组多选框；配置仍以一条 JSON Setting 保存，不创建品牌管理 CRUD 或新表。

## 4. 最小后端改动

### 4.1 域名配置解析

新增轻量 `DomainBrandResolver`：

1. 从 `Request.Host` 取得域名，规范化为小写、无端口的 Host。
2. 从 `domain_brand_config` 查找配置。
3. 未命中时使用默认 C 端配置。
4. 把配置写入 `request.Context()`，供后续 Service 读取。

不需要新增数据库表，也不需要给现有 Service 方法逐个添加 `brandID` 参数。

Nginx 继续代理到同一应用，并保留原始 Host：

```nginx
proxy_set_header Host $host;
```

### 4.2 前端公开配置

当前公开设置接口增加五项覆盖，其中站点名称和 Logo 也用于服务端 HTML 注入：

- `site_name`
- `site_logo`
- `site_subtitle`
- `contact_info`
- `api_base_url`

其他现有 Settings 保持不变。前端已能从公开配置读取站点名称和 Logo，因此只需让公开配置按 Host 返回不同值。

服务端 HTML 注入缓存必须改为按 Host 缓存。当前只有一个 HTML 缓存槽，新增域名后若不改，会出现先访问的域名把 Logo/标题缓存给另一域名的问题。

建议缓存键直接使用规范化 Host，例如：

```text
public-html:xiaohondou.com
public-html:xiaofanqie.org
```

### 4.3 分组隔离

不修改 `groups` 表，也不新增 `group.brand_id`。每个域名的分组范围完全由 `allowed_group_ids` 配置控制。

必须接入的接口和服务端校验：

| 场景 | 最小改动 |
| --- | --- |
| 获取可用分组 | 在现有活跃分组结果上按 `allowed_group_ids` 过滤 |
| 新建 API Key | 校验 `group_id` 在当前 Host 白名单内 |
| 修改 Key 分组 | 校验新 `group_id` 在当前 Host 白名单内 |
| 套餐列表与 checkout | 仅返回绑定在白名单分组上的套餐 |
| 创建订阅订单 | 服务端再次校验 `plan_id` 所属分组在当前 Host 白名单内 |

套餐和创建订单虽然不是“分组接口”，但套餐直接绑定分组。若不加这两处，用户可以构造另一个域名的 `plan_id` 完成跨域名购买，分组隔离就不完整。

用户的 API Key 列表第一版可以保持全量显示，避免对历史无分组 Key 和跨域名管理行为做迁移。创建和切换分组时严格限制即可。后续若需要页面完全不展示另一域名的 Key，再补列表过滤。

### 4.4 不变的数据面

`/v1` 等网关接口保持不读取 Host 白名单：

- API Key 的有效性仍由 Key、用户状态和绑定分组决定。
- 分组倍率仍由原有计费链路计算。
- 同一个上游账号可以绑定 C/B 两套分组。
- 两套分组使用独立倍率，但共享账号池的容量和风险。

这样不会改变现有客户端调用方式，也不需要修改 API Key 认证缓存、调度快照或网关转发代码。

## 5. 最小前端改动

继续使用同一份 Vue 构建产物：

- 登录页、侧栏和首页直接使用当前公开设置中的名称、Logo、副标题、客服联系方式和 API 端点地址。
- 不增加品牌主题系统，不复制页面，不增加品牌路由。
- API 请求仍使用相对路径，因此两个域名都请求同一后端。
- 系统设置增加域名卡片与分组选择控件，保存为 `domain_brand_config`。

第一版不要求 B/C 拥有完全不同的首页布局。后续只要继续扩展 JSON 白名单字段，就可以增加 `home_content`、文档链接或主题变量。

## 6. 配置与上线步骤

1. 开发并发布 `domain_brand_config` 解析、按 Host 的公开设置覆盖和 HTML 缓存分片。
2. 将分组列表、Key 创建/修改、套餐查询和下单校验接入同一分组白名单。
3. 配置 `xiaohondou.com`，其分组列表等于当前 C 端分组，并验证主站行为不变。
4. 配置新域名的 DNS、TLS 和 Nginx；`xiaohondou.com` 与 `xiaofanqie.org` 代理至同一应用并保留 Host。
5. 新建 B 端专用分组并给它们配置独立倍率；需要共用的上游账号追加绑定到 B 端分组。
6. 在 `domain_brand_config` 增加 `xiaofanqie.org` 及其 B 端分组 ID。
7. 将 `nideyiyi.com` 和 `api.nideyiyi.com` 以 `308` 重定向到 `xiaohondou.com`，保留路径、查询参数和非 GET 请求方法。
8. 用两个浏览器会话验证页面配置、分组选择、Key 创建和套餐购买。

## 7. 验收标准

### 默认 C 端兼容

- `xiaohondou.com` 的名称、Logo、副标题、客服信息和可用分组与原 C 端一致。
- `nideyiyi.com` 与 `api.nideyiyi.com` 永久重定向到 `xiaohondou.com`。
- 注册流程保持远端行为，不自动创建默认 API Key。
- 现有 API Key、余额、用量、订单和网关调用没有数据迁移或行为变化。
- 未配置 Host 回退默认 C 端配置。

### 新域名验证

- `xiaofanqie.org` 显示自己的名称、Logo、副标题、客服联系方式和 API 端点地址。
- A/B 两个域名交替刷新时不串标题、Logo 或 HTML ETag。
- 两个域名的“可用分组”接口只返回各自白名单分组。
- 在 A 域名提交 B 分组 ID 创建或修改 Key 时，服务端拒绝。
- 在 A 域名构造 B 套餐 ID 创建订单时，服务端拒绝。
- 同一个账号在两个域名登录后仍是同一用户和同一余额。
- 同一上游账号绑定 A/B 分组后，两个分组按各自倍率出账。

## 8. 改动量与后续升级

这个方案的代码影响主要集中在：Settings 解析与校验、Host 上下文、公开设置/HTML 缓存、分组列表、Key 写操作、套餐查询和订单校验。

未来出现以下需求时，再升级为完整品牌模型：

- B 端价格不能被 C 端用户访问。
- 按品牌统计用户、订单、收入或用量。
- B/C 端余额、支付渠道、邮件、合同或客服需要隔离。
- B 端需要独立账号池、容量和 SLA。
- 前端页面结构而非仅公开配置字段需要显著不同。

在这些需求出现前，不建议提前引入 `brands`、`brand_domains`、用户品牌成员或订单品牌字段。
