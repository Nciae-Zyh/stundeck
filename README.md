# StunDeck

StunDeck 是一个本地优先、开源的 STUN 映射控制面板。它负责监管 NATMap、把动态公网映射转发到局域网服务，并在映射变化后自动同步 Cloudflare Redirect Rules 和签名 Webhook。

> STUN 不是中继服务，也不能穿透所有 NAT。StunDeck 只自动化可行环境中的探测、保活、转发与发布，不会把对称型 NAT 或受限 CGNAT 变成公网入口。

## 当前能力

- 单节点 TCP/UDP NATMap 监管。
- 局域网目标可达性预检。
- Cloudflare API Token 验证与 Zone 选择。
- Cloudflare DNS 与 Single Redirect 单规则同步。
- 仅支持明确记录在 Single Redirect 文档中的 `302` 和 `307`。
- 映射变化事件、持久化历史与自动同步。
- HMAC-SHA256 签名 Webhook、重试与 SSRF 防护。
- Argon2id 本地管理员密码、SameSite 会话与 CSRF 防护。
- AES-256-GCM 加密保存 Cloudflare Token 和 Webhook Secret。
- Vue 3 响应式 Dashboard。
- Docker Compose、Docker 镜像和原生二进制构建。

仓库、Docker 镜像、CI 和示例文件都不包含 Cloudflare Token、Global API Key 或默认管理员密码。

## 快速开始

### Docker Compose

Linux 主机或软路由是推荐运行环境。STUN 映射需要看到真实网络栈，因此容器使用 host network，但默认不会启用 `privileged`。

```bash
docker compose up -d --build
```

打开 `http://服务器局域网IP:8080`，完成本地管理员初始化，再添加 Cloudflare API Token。

正式暴露管理页面前，请通过反向代理或 Cloudflare Tunnel 提供 HTTPS，并设置：

```yaml
environment:
  STUNDECK_SECURE_COOKIES: "true"
```

### 直接运行

需要 Go 1.25、Node.js 24、pnpm 10，以及可执行文件 `natmap`：

```bash
make bootstrap
make build
./bin/stundeck
```

`stundeck-notify` 和 `natmap` 必须位于 `PATH`，也可以通过环境变量指定完整路径。

## Cloudflare Token

不要使用 Global API Key。推荐创建仅限目标 Zone 的 API Token：

- Zone Read
- DNS Write，仅在让 StunDeck 管理 DNS 时需要
- Dynamic URL Redirects Write

详细说明见 [Cloudflare 配置](docs/cloudflare.md)。

## 工作方式

```text
NATMap mapping event
        ↓
internal authenticated callback
        ↓
SQLite desired/actual state
        ↓
Cloudflare single-rule reconcile + signed webhooks
```

StunDeck 为每个 Redirect Rule 写入稳定的 `ref`，只通过 Cloudflare 的单规则 API 更新自己的规则。DNS 记录也带有 `managed-by=stundeck:<service-id>` 注释；遇到同名非托管记录时会停止，不会接管用户已有资源。

## 网络与安全边界

- Cloudflare Redirect 只保护入口请求。浏览器收到 `Location` 后会直连公网 IP 和动态端口。
- 第二跳不会经过 Cloudflare WAF、Access 或缓存，并会暴露公网 IP/端口。
- DNS 不能保存端口，普通浏览器也不会使用 SRV 记录发现 Web 端口。
- HTTPS 直连必须配置目标域名，并让局域网服务持有覆盖该域名的有效证书。
- 敏感管理服务优先使用 Cloudflare Tunnel，而不是 STUN Redirect。
- Docker Desktop 不适合作为正式 STUN 网关；推荐 Linux host network。

## 文档

- [部署与运行](docs/deployment.md)
- [Cloudflare 配置](docs/cloudflare.md)
- [Webhook 协议](docs/webhooks.md)
- [架构说明](docs/architecture.md)
- [安全策略](SECURITY.md)

## 开源组件

StunDeck 使用 Apache-2.0 许可证。Docker 镜像包含 MIT 许可的 [NATMap](https://github.com/heiher/natmap)，版本和下载校验值固定在 Dockerfile 中；其许可证位于 [third_party/NATMap-LICENSE](third_party/NATMap-LICENSE)。

## Development status

The current release is an MVP. It is suitable for controlled self-hosted testing, but external reachability must still be verified from a different network. UPnP/NAT-PMP automation, multi-node agents, Cloudflare Tunnel fallback and Worker-based 303 responses are planned follow-up work.
