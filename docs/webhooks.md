# Webhook 协议

## 事件

当前可能发送：

- `service.created`
- `engine.started`
- `engine.start_failed`
- `engine.stopped`
- `mapping.changed`
- `cloudflare.synced`
- `cloudflare.sync_failed`
- `webhook.test`

## 请求

```http
POST /your-endpoint HTTP/1.1
Content-Type: application/json
X-StunDeck-Event-ID: event-id
X-StunDeck-Timestamp: 1700000000
X-StunDeck-Signature: v1=hex-hmac-sha256
```

```json
{
  "id": "event-id",
  "event": "mapping.changed",
  "serviceId": "service-id",
  "level": "info",
  "message": "Public mapping changed",
  "data": {
    "publicIp": "203.0.113.10",
    "publicPort": 45678,
    "protocol": "tcp"
  },
  "occurredAt": "2026-08-02T10:00:00Z"
}
```

## 签名验证

签名输入为：

```text
timestamp + "." + raw_request_body
```

使用创建 Webhook 时仅显示一次的 Secret 计算 HMAC-SHA256，并与 `X-StunDeck-Signature` 中的十六进制值做常量时间比较。接收方还应拒绝超过五分钟的时间戳，并记录 Event ID 防止重放。

## 网络限制

默认拒绝回环、RFC1918、链路本地和未指定地址，并在实际连接时重新解析 DNS，降低 DNS rebinding 风险。只有明确启用“允许访问局域网地址”后才会向私有地址投递。

Webhook 不跟随重定向，失败后指数退避，最多十次正常重试；之后进入每日低频重试，等待管理员处理。
