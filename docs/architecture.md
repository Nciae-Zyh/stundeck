# 架构说明

## 组件

```text
Vue Dashboard
      ↓ same-origin JSON + session + CSRF
Go HTTP API
      ├─ SQLite desired/actual state
      ├─ NATMap process supervisor
      ├─ Cloudflare reconciler
      └─ persistent webhook dispatcher
```

## NATMap 边界

StunDeck 不接收用户自定义 Shell 模板。每个 NATMap 子进程只调用固定的 `stundeck-notify` 二进制，映射数据通过继承的短期内部 Token回传到 `127.0.0.1`。

NATMap 的 stdout/stderr 只进入结构化运行日志。Cloudflare Token、Webhook Secret、会话 Cookie 和内部回调 Token不会被写入子进程参数或日志。

## 状态模型

服务状态：

```text
stopped → discovering → mapped → healthy
                     ↘ sync_error
         ↘ error
```

- `mapped`：已经收到 NATMap 公网映射。
- `healthy`：Cloudflare 配置已同步。
- `sync_error`：映射存在，但 Cloudflare 更新失败。
- `error`：目标不可达、二进制缺失或 NATMap 进程退出。

该状态不等同于外部可达性。可靠的公网验证需要部署在另一网络的探针。

## 数据表

- `users`、`sessions`
- `cloudflare_connections`
- `services`
- `events`
- `webhooks`
- `webhook_deliveries`

SQLite 使用 WAL、外键约束和单写连接。映射与投递状态在重启后保留；已启用服务会在控制面启动后重新拉起 NATMap。
