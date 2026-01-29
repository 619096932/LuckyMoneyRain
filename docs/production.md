# 生产部署说明

## 项目概述
“年会红包雨”是一个活动型实时互动系统，提供用户登录、红包雨游戏、排行榜/开奖、钱包提现等功能。后端使用 Go + Gin，数据持久化使用 MySQL，缓存与实时状态使用 Redis，并集成短信服务（Submail）与支付宝转账接口。

## 架构总览
- 客户端：浏览器访问 HTML 页面（`web/index.html`/`web/admin.html`/`web/wallet.html`/`web/withdraw.html`），通过 HTTP + WebSocket 与服务端交互。
- 主服务：`cmd/server`，负责 API、WebSocket、游戏逻辑、管理后台、钱包与提现请求。
- 提现后台任务：`cmd/withdraw_worker`（可独立部署）或在主服务内通过 `WITHDRAW_WORKER_ENABLED=true` 启动。
- 存储依赖：MySQL（业务数据）、Redis（会话、实时状态、排行榜/点击流等）。
- 外部依赖：Submail 短信、支付宝转账接口。

## 目录结构
- `cmd/server`：HTTP/WS 服务入口。
- `cmd/withdraw_worker`：提现后台任务入口。
- `internal/config`：配置加载（`.env`）。
- `internal/handlers`：HTTP/WS 处理、业务流程。
- `internal/game`：红包雨游戏引擎与点击校验。
- `internal/payments`：支付宝转账封装。
- `internal/sms`：Submail 短信发送。
- `internal/db`：MySQL/Redis 连接。
- `internal/models`：核心数据结构。
- `docs/api.md`：API 清单。
- `docs/schema.sql`：数据库建表脚本。
- `dist/`：构建产物示例（可选）。
- `web/`：前端静态页面（嵌入到二进制）。
- `web_assets.go`：静态页面内嵌配置。

## 数据模型（简述）
详细结构见 `docs/schema.sql`，核心表如下：
- `users`：用户基础信息（手机号、昵称、头像、管理员标记）。
- `rounds`：游戏轮次配置与状态。
- `round_whitelist`：轮次白名单。
- `scores`：用户分数（每轮）。
- `award_batches` / `award_details`：开奖批次与获奖明细。
- `wallets` / `wallet_ledger`：钱包余额与流水。
- `user_alipay_accounts`：用户绑定的支付宝信息。
- `withdraw_requests`：提现申请与支付宝回执状态。

## 关键运行流程
1. 认证登录
   - 短信验证码登录（Submail）。
   - 远程注册/登录（`/api/remote/register`）可用于外部系统对接，需 `REMOTE_API_KEY`。
   - 登录成功后发放 JWT，Redis 记录 session。
2. 游戏流程
   - 管理端创建轮次并启动。
   - 客户端通过 HTTP/WS 获取轮次与切片信息。
   - 点击事件写入 Redis Stream，并实时校验。
3. 开奖与钱包
   - 管理端触发开奖，生成批次与明细。
   - 钱包余额通过 MySQL 管理。
4. 提现
   - 用户提交提现申请，进入 `withdraw_requests`。
   - 自动提现由 worker 调度；不足余额时自动延后重试。

## 配置说明（生产必读）
配置集中在 `.env`，示例见 `.env.example`。关键项：
- `JWT_SECRET`：JWT 签名密钥，必须配置。
- `ADMIN_PASSWORD` / `ADMIN_TOKEN` / `ADMIN_PHONES`：管理员登录与权限控制。
- `INIT_SECRET`：初始化重置密钥（危险操作，请妥善保管）。
- `SUBMAIL_*`：短信服务配置。
- `ALIPAY_*`：支付宝转账与证书配置。
- `WITHDRAW_*`：提现策略与开关。
- `REMOTE_API_KEY`：远程注册接口密钥。

## 构建与部署
### 依赖
- Go 1.21
- MySQL 5.7+ / 8.0+
- Redis 6+

### 初始化数据库
1. 创建数据库并导入 `docs/schema.sql`
2. 确保字符集为 `utf8mb4`

### 构建
```bash
go build -o bin/hongbao-server ./cmd/server
go build -o bin/hongbao-withdraw-worker ./cmd/withdraw_worker
```

### 运行
1. 准备 `.env`（从 `.env.example` 复制并填写敏感项）
2. 启动服务：
```bash
./bin/hongbao-server
```
3. 可选：独立启动提现 worker（若不启用内置 worker）
```bash
./bin/hongbao-withdraw-worker
```
4. 健康检查：`GET /healthz`

## 运维与安全建议
- 严格保护 `.env`，避免泄露密钥与证书。
- `INIT_SECRET` 会触发全量数据清空，建议只在受控环境使用。
- WebSocket 默认允许任意来源连接，生产环境建议在网关/代理层限制来源。
- 如需审计点击事件，可在 Redis Stream（`round:*:clicks`）侧消费后落库。

## 参考
- API 清单：`docs/api.md`
- 数据库结构：`docs/schema.sql`
