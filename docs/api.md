# API 清单

> 基础地址：`http://<host>:<port>`
> 认证方式：`Authorization: Bearer <token>`

## 远程接入（供其他系统）

### POST `/api/remote/register`
远程注册/更新用户信息并领取 token。  
需要安全 KEY（`REMOTE_API_KEY`），可通过 `X-Remote-Key` 头或 body `key` 传入。
远程服务不可用时，前端仍可走短信登录，避免单点依赖。

请求：
```json
{
  "key": "REMOTE_API_KEY",
  "phone": "13800000000",
  "nickname": "张三",
  "avatar_url": "https://example.com/avatar.png"
}
```

响应：
```json
{
  "token": "jwt-token",
  "user": {
    "id": 1,
    "phone": "13800000000",
    "nickname": "张三",
    "avatar_url": "https://example.com/avatar.png",
    "is_admin": false
  }
}
```

## 认证

### POST `/api/auth/sms/send`
发送短信验证码。

### POST `/api/auth/sms/verify`
验证验证码并登录，返回 `token`。

## 用户

### GET `/api/user/me`
获取当前用户信息（含昵称、头像）。

### GET `/api/user/wallet`
获取钱包余额与支付宝绑定信息。

### POST `/api/user/alipay/bind`
绑定/更新支付宝账号信息。

### POST `/api/user/withdraw`
提交提现申请。

### GET `/api/user/withdraws`
提现记录列表。

## 游戏

### GET `/api/rounds/current`
当前轮次信息（公开）。

### GET `/api/game/state`
当前游戏状态（需登录）。

### POST `/api/game/click`
点击事件上报（需登录）。

### GET `/api/game/result`
获取本轮成绩（需登录）。

## WebSocket

### GET `/ws`
WebSocket 连接，支持 `?token=...`。

## 管理后台（需管理员）

### POST `/api/admin/login`
管理员登录，返回管理员 token。

### POST `/api/admin/init/reset`
初始化重置（需要 `INIT_SECRET`）。

### POST `/api/admin/rounds`
创建轮次。

### GET `/api/admin/rounds`
轮次列表。

### POST `/api/admin/rounds/:id/whitelist`
设置白名单。

### POST `/api/admin/rounds/:id/lock`
锁定轮次。

### POST `/api/admin/rounds/:id/clear`
清空轮次数据。

### POST `/api/admin/rounds/:id/start`
开始轮次。

### POST `/api/admin/rounds/:id/draw`
开奖。

### GET `/api/admin/rounds/:id/leaderboard`
排行榜。

### GET `/api/admin/rounds/:id/export`
导出轮次数据。

### GET `/api/admin/online_users`
在线用户列表。

### GET `/api/admin/metrics`
实时指标。

### GET `/api/admin/award_batches`
开奖批次列表。

### POST `/api/admin/award_batches/:id/confirm`
确认入账。

### GET `/api/admin/withdraws`
提现记录列表。

### POST `/api/admin/withdraws/:id/transfer`
手动打款。

### POST `/api/admin/withdraws/:id/sync`
同步提现状态。

### GET `/api/admin/alipay/account`
支付宝资金账户查询。

### GET `/api/admin/alipay/quota`
支付宝转账额度查询。

### POST `/api/admin/alipay/transfer_test`
支付宝转账测试。
