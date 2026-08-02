# 部署与运行

## 推荐环境

- Linux amd64 或 arm64。
- 主路由、旁路由，或能够收到外部映射流量的局域网 Linux 主机。
- Docker Engine host network，或直接运行二进制。

如果 StunDeck 不运行在主路由，仍可能需要在路由器中设置 DMZ、端口转发、UPnP 或 NAT-PMP。第一版不会主动修改系统防火墙或路由器配置。

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

如果目标平台确实要求修改 nftables，后期的防火墙适配器会使用独立 helper 和最小 `CAP_NET_ADMIN`，不会要求整个容器使用 `privileged: true`。

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
