# Cloudflare 发布与 Access

在「Cloudflare」中保存 Token 后，点击连接旁的「权限与域名」。检测会列出所选 Zone 的域名、各功能权限、Access 条件和本机 cloudflared 是否可用。Token 更新仍保留原连接 ID，因此不需要重建服务；更新时不能改变连接所属 Zone。

## 发布方式

| 方式 | 流量路径与适用条件 | Access |
| --- | --- | --- |
| DNS 直连 | NATMap 获取公网映射，自动更新 DNS only A / AAAA；访问时仍需公网端口 | 不支持，流量不经过 Cloudflare |
| Redirect | Cloudflare 302 / 307 跳转到公网 IP 或源站域名与动态端口 | 不支持保护第二跳 |
| HTTP 代理 | 自动同步 Proxied A / AAAA，Cloudflare 回连公网映射；必须是 HTTP / HTTPS，且映射端口受限制 | 本面板仅允许 HTTPS 443 服务配置；源站需要限制绕过代理的直连 |
| Tunnel | Cloudflared 从运行 StunDeck 的主机主动连接 Cloudflare，直接转发到局域网目标；无需 STUN、UPnP 或公网入站 | 支持按域名配置 |
| Quick Tunnel | 无需账户或域名，启动后显示随机 trycloudflare.com 临时网址；支持 HTTP / HTTPS | 临时域名不能在这里配置 Access |
| WARP 私网 | cloudflared + 私网 CIDR 路由，客户端通过 Cloudflare One 访问 TCP / UDP，无需公网入站 | 使用 Gateway 网络策略；不创建域名 Access 应用 |
| Workers | 固定 HTTPS 入口 → Worker → DNS only 源站与 NATMap 动态端口；需公网可回连 | 入口支持；源站需防直连 |
| Spectrum | Cloudflare TCP / UDP 边缘端口转发到 NATMap 公网映射；需有相应协议的套餐与配额 | 本面板不为 Spectrum 创建 HTTP Access 应用 |

「仅公网映射」仍保留原来的 NATMap 工作方式。普通 HTTP 代理不会把任意公网端口自动转换成 443。当前允许的端口按 [Cloudflare Network ports](https://developers.cloudflare.com/fundamentals/reference/network-ports/) 校验：

- HTTP：80、8080、8880、2052、2082、2086、2095。
- HTTPS：443、2053、2083、2087、2096、8443。

代理同步前会先验证映射端口，不支持时显示错误，并建议使用 Tunnel / Redirect。HTTPS 源站须有正确证书，并在 Cloudflare 配置与源站匹配的 SSL/TLS 模式（建议 Full strict）；面板不自动改变全 Zone 的 TLS 设置。

Tunnel 支持 HTTP、HTTPS、TCP、SSH、RDP。浏览器可以直接访问 HTTP / HTTPS 入口；TCP / SSH / RDP 使用 [cloudflared 访问客户端](https://developers.cloudflare.com/tunnel/concepts/routing/)。公网 hostname Tunnel 不提供裸 UDP 端口。Quick Tunnel、WARP 私网和 Workers 也可直接在发布方式中选择，具体条件见下文。

## Token 最小权限

仅支持 API Token，不支持 Global API Key。按所用功能授权：

| 功能 | 权限 | 资源范围 |
| --- | --- | --- |
| 列出 Zone | Zone > Zone > Read | 需要管理的 Specific zone |
| DNS / HTTP 代理 | Zone > DNS > Edit | 对应 Zone |
| Redirect | Zone > Single Redirect > Edit；启用自动 DNS 时另需 DNS Edit | 对应 Zone |
| Tunnel | Account > Cloudflare Tunnel > Edit；Zone > DNS > Edit | Zone 所属的指定 Account，以及对应 Zone |
| WARP | Account > Cloudflare Tunnel > Edit；私网路由另接受 Cloudflare One Networks > Edit | 指定 Account，无需 DNS 权限；连接仍用 Zone Read 获取账户 ID |
| Workers | Account > Workers Scripts > Edit；Zone > Workers Routes > Edit、DNS > Edit | 指定 Account 和 Zone |
| Quick Tunnel | 不需要 API Token | 不需要自有域名 |
| Access | Account 或 Zone > Access: Apps and Policies > Edit | 对应 Account 或 Zone；程序先尝试账户接口，再在无访问权限时尝试 Zone 接口 |
| Spectrum | Zone > Zone Settings > Edit；DNS Read 用于检查域名冲突 | 对应 Zone；还需 Cloudflare 套餐授权 |

也兼容 Cloudflare API 所接受的 `Cloudflare One Connectors Write` / `Cloudflare One Connector: cloudflared Write` Tunnel 权限。界面的 Edit 与 API 文档中的 Write 对应。权限名称以 [Cloudflare 当前权限列表](https://developers.cloudflare.com/fundamentals/api/reference/permissions/) 为准。

不需要 API Tokens Edit，也无需为了权限检测额外授予 API Tokens Read。Token 先验证有效状态，支持用户 Token 与账户 Token 的相应 verify 接口。账户 ID 从 Cloudflare 的 Zone 返回值获取，不能在表单中指定其他账户。API Token 在 SQLite 中以 AES-256-GCM 加密保存，不返回浏览器。

## 权限判断与域名清单

检测只执行 GET，不会创建资源来试探写权限。状态含义：

- **已确认写权限**：Zone 返回有效 DNS edit 权限，或当前 Token 的可读取策略明确授予相应资源的 Write。
- **仅可读取**：可读取的 Token 策略只包含相应 Read。
- **写权限待确认**：功能可读，但无法取得明确的写权限证据；实际提交配置时由 Cloudflare 校验。
- **不可用**：API 拒绝访问，或资源未开通 / 不可见；显示所需权限及资源范围提示。
- **检测失败**：网络、限流、服务异常等；不能当作没有权限。

Token 有效、GET 成功或列表为空，都不能证明可写；Spectrum 的读取成功也不能证明某个 TCP / UDP 协议已获得套餐授权。

Zone、DNS、Access 应用列表按分页完整读取，超出边界或读取失败会报错，不会把不完整清单当成不存在。域名清单仅包含本 Zone 的 A、AAAA、CNAME，并说明 DNS only、未激活 Zone、通配符、已有 Access 应用覆盖、Redirect 或不兼容服务等限制。新域名在服务配置中填写，由发布操作创建 DNS；面板不会接管已有的外部 DNS。

## Tunnel 生命周期

每个 Tunnel 服务使用独立的远程管理 Tunnel，名称为 `stundeck-<service-id>`。云端 ID 在创建成功后立即保存，配置失败后重试复用同一 ID。Ingress 仅含该服务 hostname 和最后一条 `http_status:404`，避免把未匹配域名转发到局域网。

「启动」同步 Tunnel、绑定 `<tunnel-id>.cfargotunnel.com` 的 Proxied CNAME，然后运行 cloudflared。Connector token 仅在服务启动时获取，通过子进程 `TUNNEL_TOKEN` 环境变量传递，不进入命令行参数、事件日志或 HTTP 响应。HTTPS 本地目标保持证书验证。

- Docker 镜像内置固定版本、校验 SHA-256 的 cloudflared，fnOS FPK 使用相同容器镜像。
- 二进制部署需要另行安装官方 cloudflared，并保证在 PATH 中；或设置 `STUNDECK_CLOUDFLARED_BINARY`。
- 容器中 `localhost` 指容器自身；访问宿主机或其他局域网服务时按部署网络填写地址。fnOS / host 网络部署保持原有行为。
- 进程由应用生命周期管理，HTTP 请求结束不会停止它；停止服务终止本地 connector，应用重启会恢复已启用服务。
- 「Tunnel 进程运行中」表示本机进程仍在，不表示云端已连接或公网请求已经成功。需要在 Cloudflare 核对 Tunnel 状态并从外网验证。

## Quick Tunnel 临时分享

选择 Quick Tunnel，填写本地 HTTP / HTTPS 服务后启动。界面在 cloudflared 返回地址后显示临时网址；停止或退出会清空旧网址，再次启动通常获得新网址。只提取明确的 `https://<随机名称>.trycloudflare.com`，不会转发原始 connector 日志。进程使用独立空配置，并剔除继承的 Tunnel token 等环境变量。

Quick Tunnel 用于测试和临时分享，没有 SLA，不支持 SSE，当前最多 200 个并发请求。需要稳定域名、Access 或 SSE 时使用命名 Tunnel。见 [Quick Tunnels](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/do-more-with-tunnels/trycloudflare/)。

## WARP 私网路由

目标填写私网 IP。CIDR 留空时只发布该主机的 `/32` 或 `/128`；显式 CIDR 必须规范化、包含目标 IP，并完全处于 RFC 1918 / IPv6 ULA 范围。拒绝公网、回环和默认路由。路由覆盖 CIDR 中的所有端口，并不只覆盖服务表单中的端口。

每个服务建立远程管理 Tunnel，启用 `warp-routing`，配置默认 404 ingress，通过当前 `POST /accounts/{account_id}/teamnet/routes` 接口建立路由并保存 ID。后续按 route ID 清理，不使用即将移除的 CIDR 路径接口。检测所有现有路由的重叠，冲突时拒绝接管；本面板使用默认虚拟网络。

访问端需注册到同一 Zero Trust 组织，选用对应虚拟网络，并让 Split Tunnels 包含该 CIDR（Include 模式添加；Exclude 模式移除排除）。必须配置适当的 Gateway 网络访问策略。面板不修改整个组织的客户端注册、分流或身份策略。普通域名 Access 表单不适用于私网 IP。参考 [API 创建 Tunnel 与网络路由](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/get-started/create-remote-tunnel-api/) 和 [Split Tunnels](https://developers.cloudflare.com/cloudflare-one/team-and-resources/devices/cloudflare-one-client/configure/route-traffic/split-tunnels/)。

## Workers HTTPS 代理

填写同 Zone 下两个独立且未被占用的域名：入口和源站。StunDeck 自动部署带所有权标签的 Worker、入口精确路由、代理入口 DNS 与 DNS only 源站记录。源站地址固定为配置的域名与当前公网映射端口，不接受请求参数指定转发目标。公网映射变化时更新 DNS 和脚本。需要源站可被公网回连；HTTPS 源站证书必须匹配源站域名。

Worker 使用明确的兼容日期和 `allow_custom_ports`，保留路径、查询和流式请求 / 响应，支持 WebSocket；不跟随源站重定向，不把认证信息继续发到第三方，源站自身的重定向会重写到入口域名。子请求和响应禁止共享缓存，失败返回通用 502。关闭 workers.dev 和预览网址，并校验入口主机名，避免旁路入口域名的 Access。拒绝覆盖已有同名脚本、重叠路由或外部 DNS。

Workers 入口可以配置 Access，但 DNS only 源站仍可被直连，需配置源站防火墙或应用认证。应用自身生成的绝对 URL、Cookie Domain 和公开基址仍需按入口域名配置。使用受 Workers 套餐 / 配额限制；不会自动升级套餐。参考 [自定义端口行为](https://developers.cloudflare.com/workers/configuration/compatibility-flags/#allow-specifying-a-custom-port-when-making-a-subrequest-with-the-fetch-api)、[Workers 路由](https://developers.cloudflare.com/workers/configuration/routing/routes/) 和 [Workers 最佳实践](https://developers.cloudflare.com/workers/best-practices/workers-best-practices/)。

## 按域名配置 Access

先在 Cloudflare Zero Trust 配置身份提供商，例如邮箱验证码或已有 SSO。在连接的「权限与域名」中选择符合条件的域名，填写允许登录的完整邮箱和会话时长，提交后创建 `self_hosted` 应用及邮箱允许策略。

创建应用时就在同一请求中携带允许策略。未匹配用户默认拒绝，不创建 Everyone 或 Bypass。保存后回读应用与策略，验证域名、时长及邮箱集合；回读失败会明确显示“已写入但未确认”，并保留本地所有权，供刷新和重试。

仅允许管理本实例创建并保存 ID 的应用。已有的域名、通配符或路径应用覆盖会阻止新建，避免覆盖原有认证配置。更新邮箱时保留已有身份提供商、应用级 MFA 和 Cookie 绑定设置；存在外部附加条件的策略拒绝覆盖。外部改动应用用途、多域名或策略数量后，面板拒绝覆盖，需先在 Cloudflare 整理。

Access 保护的是经过 Cloudflare 的请求：DNS only、Redirect 第二跳、Spectrum 不会因此受到登录保护。普通代理和 Workers 还应限制源站直连。已停止或清理 Tunnel 不会移除 Access；要解除保护，必须在 Access 应用旁明确选择「移除保护」。

## 资源所有权与清理

自动 DNS 仅修改带有 `managed-by=stundeck:<service-id>` 注释的单条记录，同名外部记录、多记录或记录类型冲突均报错。Redirect 使用稳定的 `stundeck_<service-id>` ref，通过单规则 API 更新，不替换完整 ruleset。

Tunnel、私网路由、Workers、Spectrum、Redirect / DNS 和 Access 所有权保存在新增表中，升级自动迁移，旧服务和原有映射保持。更换已发布服务的方式、连接、域名或私网 CIDR 前，先停止，再选择「清理云端发布」。该操作仅删除此服务拥有的 DNS、Tunnel、私网路由、Workers、Redirect 或 Spectrum，保留独立的 Access 策略。连接仍有关联服务或托管资源时不能删除。

Spectrum 同步公网映射时保留已有 TLS、防火墙、Proxy Protocol 和边缘 IP 配置。Spectrum 需要用户明确选择并确认已核对套餐与费用。StunDeck 不自动购买套餐、升级账户或建立外部付费订阅。

## 开发验证

相关回归覆盖：分页、只读与未知权限、资源范围、网络失败、DNS 所有权、Tunnel 部分失败后重试、Access 策略与回读、外部应用冲突、代理端口、Spectrum 所有权、旧库迁移、Quick URL 生命周期、私网 CIDR 边界 / 重叠、Workers 部分失败重试 / 固定源站 / 流式代理 / 路由所有权、connector token 隔离、进程启停、接口鉴权以及前端表单联动。Mock API 与本地界面验证不代表真实 Cloudflare 账户、套餐和外网链路已联调通过。
