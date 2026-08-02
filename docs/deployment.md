# 部署与运行

## 推荐环境

- Linux amd64 或 arm64。
- 主路由、旁路由，或能够收到外部映射流量的局域网 Linux 主机。
- Docker Engine host network，或直接运行二进制。

如果 StunDeck 不运行在主路由，仍可能需要在路由器中设置 DMZ、端口转发、UPnP 或 NAT-PMP。服务默认不管理路由器；选择 UPnP/NAT-PMP 后，StunDeck 才会为该服务下发和撤销端口映射。

## 路由器端口映射

NATMap 获取到公网地址并不等于任意公网来源都能回连。局域网部署时可以在服务编辑页选择：

- `UPnP`：推荐。自动发现 IGD，也可填写网关 IP 限制发现结果。
- `NAT-PMP`：向指定或自动发现的默认网关申请同一公网端口。
- `不自动管理`：适用于 StunDeck 直接运行在主路由，或已经配置 DMZ/手动端口转发的环境。

容器必须使用 host network 才能正确发现局域网 UPnP 网关。多路由、多个出口或旁路由环境中建议明确填写真实出网网关；管理页“STUN 检测”会进行只读的 UPnP/NAT-PMP 能力探测，并单独显示端口映射是否已下发。

UPnP/NAT-PMP 会修改路由器端口映射，应只在受信任的局域网中启用。异常断电可能让部分路由器暂时保留旧映射，可在路由器后台按 `StunDeck` 描述清理。

## Docker

```bash
docker compose up -d --build
docker compose logs -f stundeck
```

Compose 配置具备以下默认值：

- `network_mode: host`
- `read_only: true`
- 删除全部 Linux capabilities
- `no-new-privileges`
- 仅持久化 `/var/lib/stundeck`
- 不包含任何 Cloudflare 凭据
- 显式清空 HTTP、HTTPS、ALL_PROXY 及其小写形式

StunDeck 的运行容器应保持无代理环境。镜像拉取时临时使用的代理不要传入运行容器，也不要长期保留在 Docker 服务中；管理页的“STUN 检测”会再次检查运行时代理变量。

如果目标平台确实要求修改 nftables，后期的防火墙适配器会使用独立 helper 和最小 `CAP_NET_ADMIN`，不会要求整个容器使用 `privileged: true`。

## 控制面访问模式

首次初始化只能从本机或局域网完成。向导内可以选择：

- `local`：只接受回环地址请求。
- `lan`：接受回环、RFC 1918 私网和链路本地地址，默认推荐。
- `public`：接受公网来源；必须额外使用 HTTPS、域名/IP 白名单和 TOTP 2FA。

访问模式不会替代监听地址或主机防火墙。原生运行默认只监听 `127.0.0.1:8080`；Docker Compose 为 host network 场景监听 `0.0.0.0:8080`，再由 StunDeck 策略限制请求来源。配置反向代理或 Tunnel 时，StunDeck 看到的是代理连接地址，因此必须同时配置允许访问的 Host，并在代理层限制来源。

允许 Host 支持精确域名、IP 和 `*.example.com` 形式的子域通配符。回环健康检查不受 Host 白名单影响，避免容器被错误标记为不健康。

## 两步验证

登录后进入“控制面安全”，生成 TOTP 密钥，在 1Password、Google Authenticator 或兼容验证器中添加，再输入 6 位动态代码确认。密钥使用与 Cloudflare Token 相同的 AES-256-GCM 本地主密钥加密保存。关闭 2FA 时必须同时提供当前密码和动态代码。

## 原生运行

```bash
export STUNDECK_LISTEN=127.0.0.1:8080
export STUNDECK_DATA_DIR=./data
export STUNDECK_NATMAP_BINARY=/usr/local/bin/natmap
export STUNDECK_NOTIFY_BINARY=/usr/local/bin/stundeck-notify
./stundeck
```

通过局域网访问时，将监听地址改为 `0.0.0.0:8080`，并确保本地管理员密码足够强。不要直接把管理端口暴露到互联网。

## 数据与备份

数据目录包含：

- `stundeck.db`：SQLite 数据库。
- `master.key`：本地生成的 32 字节加密主密钥，权限为 `0600`。

必须一起备份数据库和主密钥。丢失主密钥后，已保存的 Cloudflare Token 和 Webhook Secret 无法恢复。

## 健康检查

```bash
stundeck healthcheck http://127.0.0.1:8080/api/v1/health
```

健康检查只证明控制面可响应，不代表公网映射已从外部网络验证。
