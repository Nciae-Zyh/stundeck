# 部署与运行

## 推荐环境

- Linux amd64 或 arm64。
- 主路由、旁路由，或能够收到外部映射流量的局域网 Linux 主机。
- Docker Engine host network，或直接运行二进制。

如果 StunDeck 不运行在主路由，仍可能需要在路由器中设置 DMZ、端口转发、UPnP 或 NAT-PMP。服务默认不管理路由器；选择 UPnP/NAT-PMP 后，StunDeck 才会为该服务下发和撤销端口映射。

## 路由器端口映射

NATMap 获取到公网地址并不等于任意公网来源都能回连。局域网部署时可以在服务编辑页选择：

- `UPnP`：推荐。自动发现 IGD，也可填写网关 IP 限制发现结果；按 Lucky 的方式将第一层路由器的穿透监听端口映射到同一本地端口。
- `NAT-PMP`：向指定或自动发现的默认网关申请同一公网端口。
- `不自动管理`：适用于 StunDeck 直接运行在主路由，或已经配置 DMZ/手动端口转发的环境。

容器必须使用 host network 才能正确发现局域网 UPnP 网关。多路由、多个出口或旁路由环境中建议明确填写真实出网网关；管理页“STUN 检测”会进行只读的 UPnP/NAT-PMP 能力探测，并单独显示端口映射是否已下发。

UPnP/NAT-PMP 会修改路由器端口映射，应只在受信任的局域网中启用。异常断电可能让部分路由器暂时保留旧映射，可在路由器后台按 `StunDeck` 描述清理。

## NAT 与 STUN 检测

首页会在登录后执行一次只读网络诊断，并可手动重新检测：

- 分别检查 UDP 与 TCP STUN Binding。
- 当 STUN 服务器提供 `OTHER-ADDRESS` 时，按 RFC 5780 区分公网直连、端点独立、地址依赖、地址和端口依赖映射。
- 如果服务器不提供 RFC 5780 扩展，仍会报告 STUN 是否可用以及是否检测到 NAT，但不会用旧式“全锥形/对称型”标签猜测详细类型。

本机检测只能描述当前出站映射行为，不能证明任意公网来源可以回连。最终入口仍需手机蜂窝网络或独立外部探针复核。

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

## 飞牛 fnOS FPK

FPK 模式与普通模式运行同一个容器镜像和应用二进制，只改变平台负责的目录与配置来源：

| 用途 | 普通 Docker | fnOS FPK | FPK 容器内路径 |
| --- | --- | --- | --- |
| 持久化数据 | Docker volume 或宿主机目录 | `${TRIM_PKGVAR}/data` | `/var/lib/stundeck/data` |
| SQLite | `STUNDECK_DATA_DIR/stundeck.db` | `${TRIM_PKGVAR}/data/stundeck.db` | `/var/lib/stundeck/data/stundeck.db` |
| 主密钥 | `STUNDECK_DATA_DIR/master.key` | `${TRIM_PKGVAR}/data/master.key` | `/var/lib/stundeck/data/master.key` |
| 运行配置 | Compose `environment` | `${TRIM_PKGETC}/stundeck.env` | 入口脚本导出的环境变量 |

FPK 不申请用户共享目录权限，因为数据库和主密钥包含敏感状态。fnOS 应用 ID 固定为 `stundeck-fpk`，与普通 Compose 的 `stundeck` 项目名隔离，避免安装、失败回滚或卸载清理影响普通模式。fnOS 创建 Docker 项目时，包用户尚不能创建 `${TRIM_PKGETC}`/`${TRIM_PKGVAR}`，所以生命周期脚本用 root 完成受限的安装目录初始化：在 `20000-59999` 中随机选择空闲 TCP 端口、原子记录到 `${TRIM_PKGETC}/stundeck.env` 和 `${TRIM_PKGETC}/stundeck.port`，然后立即将配置和数据目录交还 `${TRIM_UID}:${TRIM_GID}`。部分 fnOS 版本升级时会通过 root 拥有的暂存区复制旧数据，升级回调会递归恢复包用户所有权。长期运行的容器显式使用该 UID/GID，并保留只读根文件系统、capability 清空与 `no-new-privileges`。升级和重启复用已记录端口，桌面入口通过 fnOS CGI 读取记录后跳转。安装向导只设置时区和安全 Cookie；管理员、访问模式、Cloudflare Token 与 Webhook 继续通过 StunDeck 自身的首次设置和管理页面配置。

构建、安装、升级和排障步骤见 [飞牛 fnOS FPK 打包](../scripts/fpk/README.md)。

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


## Cloudflare Tunnel

Docker / fnOS 的 StunDeck 镜像内置 cloudflared。普通二进制部署需要官方 cloudflared，在 PATH 中可用或通过 `STUNDECK_CLOUDFLARED_BINARY` 指定。Tunnel / Quick Tunnel / WARP 服务不依赖 NATMap、UPnP / NAT-PMP 或入站端口；只需运行环境能访问 Cloudflare 以及所配置的局域网服务。

创建带 Tunnel Edit 和指定 Zone DNS Edit 的连接，选择 Tunnel 发布并填写本地协议、地址与端口，启动后自动创建 Tunnel 和 DNS。具体权限、Access 与清理流程见 [Cloudflare 配置](cloudflare.md)。

Quick Tunnel 无需 Token，生成临时公网网址。WARP 私网需要已注册的 Cloudflare One 客户端、Split Tunnels 与 Gateway 策略；不创建公网入口 DNS。Workers 代理使用 NATMap 公网映射，需要外部可回连，并增加 Workers Scripts / Workers Routes 权限。
