# CC Switch 导入后的余额查询

站点通过 `frontend/src/utils/ccswitchImport.ts` 生成导入链接，链接携带
`usageEnabled=true`、余额脚本和自动查询间隔。脚本使用当前 API Key 查询
`GET /v1/usage`，不需要站点登录令牌。

旧版导入脚本直接将 `/v1/usage` 拼在供应商地址后面。如果供应商地址已经
以 `/v1` 结尾，最终会请求 `/v1/v1/usage`。2026-10-21 的只读排查在生产
访问日志中确认该路径返回 404；同一本站 Key 对照请求 `/v1/usage` 返回
200，响应包含 `isValid`、`remaining`、`unit`。

当前导入脚本已去除重复的 `/v1`，但更新站点不会修改用户电脑里以前保存
的脚本。因此控制节点还提供一个精确的只读别名 `GET /v1/v1/usage`，在
鉴权前将请求路径规范为 `/v1/usage`，并复用相同的鉴权、中间件和处理器。
此处的顺序保证余额为零、Key 过期或额度耗尽时仍可查询自身余额和用量；
未认证、无效、停用的 Key，以及停用的用户和分组继续被拒绝。

这个别名只覆盖日志中确认的 GET 路径，不扩展到推理接口或更多重复前缀。
Responses 子节点不注册余额接口，余额查询仍由控制节点提供。

排查时分别确认：

1. 卡片是否配置并启用了用量脚本。没有脚本的手动配置可从当前站点重新导入，
   或在 CC Switch 的“配置用量查询”中使用 Sub2API 模板。
2. 请求路径、HTTP 状态及响应是否为 JSON。`/v1/v1/usage` 的旧脚本问题与
   Codex 官方账号配额不足导致无法发送消息是两个独立问题。
3. 回归测试须走真实 API Key 鉴权，覆盖标准路径和别名，以及正常钱包、
   零余额、过期 Key、耗尽 Key 和停用状态。

执行相关测试：

```sh
cd backend
go test ./internal/server/routes -run 'TestGatewayRoutesCCSwitchUsage' -count=1
```

代码交付后仍须按发布流程上线，才能让生产中的旧脚本使用该别名。
