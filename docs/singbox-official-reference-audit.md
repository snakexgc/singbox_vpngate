# sing-box 官方依据与防泄漏边界审计

核对日期：2026-11-17。本文保存官方要求和**审计开始时**配置的对照；具体修复及测试结果见[本轮主审计报告](singbox-security-audit.md)，并结合本轮代码差异阅读。研究过程中没有启动 TUN、改变系统代理、路由或防火墙。

## 版本基线

官方发布 API 与发布页核实：稳定版为 **v1.14.4**，发布于 2026-10-24 01:04:25 UTC；最新预发行版为 **v1.15.0-alpha.13**，发布于同日 01:08:35 UTC。在线文档已经混入 1.15 的说明，因此下文主要引用固定在 v1.14.4 标签的文档、源码。1.15 的变化单列，不当成稳定版行为。[官方稳定版 API](https://api.github.com/repos/SagerNet/sing-box/releases/latest)、[官方发布列表](https://github.com/SagerNet/sing-box/releases)。

本仓库没有在 `go.mod` 链接 sing-box，而是运行外部内核。审计开始时 `README.md` 要求支持原生 OpenVPN、含 `with_gvisor` 的 1.14+；`scripts/build-vpngate.ps1` 固定使用 1.14.4；`internal/vpngateruntime/core_manager.go` 也检查 `with_openvpn`，但版本范围允许更新版本和 Alpha。官方原生 `openvpn-client` 自 1.14.0 提供。不能把“满足最低版本和构建标签”当成未来所有版本均已兼容。[v1.14.4 OpenVPN 文档](https://github.com/SagerNet/sing-box/blob/v1.14.4/docs/configuration/endpoint/openvpn-client.md)。

`with_gvisor` 要求必须区分内核代际：1.14.4 的 `system:false` OpenVPN 设备由构建标签控制，缺少时返回明确错误；1.15 Alpha 已改用通用设备层，直接创建 sing-tun 自有 `Go` 栈，不应再一律以缺少 `with_gvisor` 拒绝官方 Alpha。仍应要求 OpenVPN 能力并实际执行配置检查和数据链路回归。[1.14.4 缺少标签时的错误](https://github.com/SagerNet/sing-box/blob/v1.14.4/transport/openvpn/device_stack_stub.go)、[Alpha 设备层选择与 newStack](https://github.com/SagerNet/sing-box/blob/v1.15.0-alpha.13/transport/device/device.go#L64)、[Alpha OpenVPN 使用该设备层](https://github.com/SagerNet/sing-box/blob/v1.15.0-alpha.13/protocol/openvpn/client.go#L617)。

| 配置项 | 1.14.4 稳定版 | 1.15 Alpha / 后续迁移 |
| --- | --- | --- |
| `tun.stack: mixed` | 支持；有 `with_gvisor` 时也是省略该项的默认行为 | 1.15 弃用整个 `stack` 选项，文档计划 1.17 删除；1.15 Alpha 源码仍支持 `mixed` 和 `gvisor`，并非已经删除 |
| 省略 `tun.stack` | 按构建能力选择默认栈 | 1.15 选择 sing-tun 自有栈；这是两代都支持的迁移形式 |
| `dns.independent_cache` | 1.14 已弃用 | 文档计划 1.16 删除；官方迁移要求移除字段 |
| `tun.dns_mode` / `dns_address` | 1.14 已支持 | 不需要为使用这两个字段升级至 Alpha |

依据：[稳定版 TUN 文档](https://github.com/SagerNet/sing-box/blob/v1.14.4/docs/configuration/inbound/tun.md#stack)、[固定 Alpha 文档](https://github.com/SagerNet/sing-box/blob/v1.15.0-alpha.13/docs/configuration/inbound/tun.md#stack)、[Alpha 实际栈选择源码](https://github.com/SagerNet/sing-tun/blob/611d83b7a57b/stack.go#L47)、[独立 DNS 缓存迁移](https://github.com/SagerNet/sing-box/blob/v1.14.4/docs/migration.md#migrate-independent-dns-cache)。这些删除版本是官方计划，不是已发布版本的运行验证。

## TUN：正确的基础配置与限制

初始生成器提供 IPv4、IPv6 TUN 地址，以及 `auto_route: true`、`strict_route: true`、`route.auto_detect_interface: true`，符合官方基础要求：`auto_route` 设置默认路由；自动探测接口为内核自己的出站绑定默认网卡，避免再次进入 TUN；Windows 的严格路由用于处理不支持的流量及多网卡 DNS 泄漏。它不会自动把显式 `direct` 改为 VPN。[稳定版 TUN 文档](https://github.com/SagerNet/sing-box/blob/v1.14.4/docs/configuration/inbound/tun.md#auto_route)、[稳定版路由文档](https://github.com/SagerNet/sing-box/blob/v1.14.4/docs/configuration/route/index.md#auto_detect_interface)。

1.14 的默认 `dns_mode` 在 `auto_route` 开启时为 `hijack`。该模式设置原生接口 DNS；省略 `dns_address` 时，使用 TUN 首个 IPv4、IPv6 地址的下一个地址，自动按 DNS 劫持处理。Windows 配合 `strict_route` 阻止通过其他接口发往 53 端口的流量。因此，初始配置省略 `dns_mode` **不等于 DNS 劫持没有开启**；显式写出 `hijack` 能让策略更易审查。[稳定版 DNS 模式说明](https://github.com/SagerNet/sing-box/blob/v1.14.4/docs/configuration/inbound/tun.md#dns_mode)。

`route_exclude_address` 是系统路由层面的排除，和“流量进入 TUN 后通过业务规则选择 direct”不同。当前业务私网规则不需要依靠大范围 `route_exclude_address` 才能直连；额外排除会让对应流量绕过路由规则及 DNS 劫持。Windows 不适用 Linux 专有的 `auto_redirect`、UID、接口排除等方案。[稳定版 TUN 排除路由说明](https://github.com/SagerNet/sing-box/blob/v1.14.4/docs/configuration/inbound/tun.md#route_exclude_address)。

Windows 严格路由不是跨崩溃的断网开关。1.14.4 使用的 sing-tun 创建动态 WFP 会话，允许当前 sing-box 进程和 TUN 接口，另对 53 端口建立阻断；关闭时释放 WFP 引擎。初始配置包含双栈地址时，不能据此宣称所有物理接口上的公网连接均被防火墙永久阻断。[1.14.4 固定依赖版本](https://github.com/SagerNet/sing-box/blob/v1.14.4/go.mod)、[动态 WFP 会话](https://github.com/SagerNet/sing-tun/blob/040db71fcd33/tun_windows.go#L213)、[本进程及 TUN 放行与 DNS 阻断](https://github.com/SagerNet/sing-tun/blob/040db71fcd33/tun_windows.go#L243)、[关闭时释放](https://github.com/SagerNet/sing-tun/blob/040db71fcd33/tun_windows.go#L627)。

此外，官方明确记录 Windows 对 DHCP option 121 注入更高优先级路由的 TunnelVision 问题尚无解决方案。`auto_route + strict_route` 不足以承诺在不可信局域网、路由被其他程序修改等条件下绝不绕过 TUN。[稳定版 TunnelVision 说明](https://github.com/SagerNet/sing-box/blob/v1.14.4/docs/manual/misc/tunnelvision.md#windows)。

## DNS：传输、解析器选择和直连例外

生成器使用 1.12+ 的新 DNS 格式（`type: tcp`），没有继续使用旧式 `address: tcp://...`。新格式未指定 `detour` 时使用普通直连拨号器，**不会自动继承 `route.final`**。因此，初始 `direct-dns` 的 `223.5.5.5:53` 是明确的直连 TCP DNS；`vpn-dns` 指定 `detour: vpngate` 才保证查询通过 VPN；域名 OpenVPN 服务器的 `bootstrap-dns` 指定 `detour: upstream`，通过外部代理引导解析。[稳定版 TCP DNS 文档](https://github.com/SagerNet/sing-box/blob/v1.14.4/docs/configuration/dns/server/tcp.md)。

TCP DNS 解决传输可靠性，不提供 DNS 内容加密。直连公网 TCP/53 会向网络路径及解析器暴露查询内容；经 VPN 的 TCP/53 在本机至 VPN 节点段受隧道保护，VPN 出口至解析器段仍为普通 DNS。若要求 DNS 传输也加密，应采用官方 `https` / `tls` DNS，并保留正确的 `detour`、证书身份和引导解析，避免引入解析环路。[官方 HTTPS DNS 格式](https://sing-box.sagernet.org/configuration/dns/server/https/)。这是由协议与配置推导的边界，不代表本轮进行了公网抓包。

初始生产 DNS 规则还把外部代理进程、外部上游域名、`localhost`、`.local`、`.lan` 送到 `direct-dns`；大陆直连和黑名单默认直连部分也使用该服务器。这些都是全局 DNS 最终出口之前的例外。向公网解析器查询 `.local` / `.lan` 不会自动变为局域网解析，可能暴露内部名称。若需要局域网名称，应选择受控的局域网解析方案或明确拒绝无意义的公网查询，不能把此行为描述成“全局所有 DNS 都经 VPN”。

`route.default_domain_resolver: direct-dns` 需要按调用位置理解。官方 Dial Fields 规定它用于普通拨号器解析服务器地址；direct 出站则解析请求目标。不能仅凭该字段判定 OpenVPN 的业务域名会直连查询。[官方 Dial Fields](https://github.com/SagerNet/sing-box/blob/v1.14.4/docs/configuration/shared/dial.md#domain_resolver)。

实际 1.14.4 OpenVPN 源码区分两种解析：OpenVPN 远端服务器使用配置得到的 `queryOptions`；已经选中 VPN 出站的业务目标域名调用 DNS Router，传入空 `DNSQueryOptions`，因而按 DNS 路由和 `dns.final` 选择解析器。全局普通域名走 `vpn-dns`，不是被默认服务器解析器无条件改送 `direct-dns`。[服务器解析源码](https://github.com/SagerNet/sing-box/blob/v1.14.4/protocol/openvpn/client.go#L476)、[业务目标解析源码](https://github.com/SagerNet/sing-box/blob/v1.14.4/protocol/openvpn/client.go#L775)、[DNS Router Lookup](https://github.com/SagerNet/sing-box/blob/v1.14.4/dns/router.go#L1249)。

`dns.strategy: ipv4_only` 是内核 DNS 解析策略，不是操作系统全局禁用 IPv6。它不会让应用预缓存的 AAAA、硬编码 IPv6、应用自带解析自动消失。必须分别检验 TUN IPv6 捕获和路由拒绝；初始 global / bypass-cn 对未先命中直连例外的 IPv6 有拒绝，blacklist 只拒绝已匹配黑名单的 IPv6。私网和上游直连规则在前，属于显式例外。[DNS strategy 官方定义](https://github.com/SagerNet/sing-box/blob/v1.14.4/docs/configuration/dns/index.md#strategy)、[路由 IP 版本条件](https://github.com/SagerNet/sing-box/blob/v1.14.4/docs/configuration/route/rule.md#ip_version)。

## 域名分流：reverse_mapping、sniff 和 FakeIP

`reverse_mapping: true` 只在内核看到 DNS 响应之后建立 IP 到名称的映射；1.14.4 源码使用 1024 项、按响应 TTL 过期的缓存，而且按 IP 保存名称。它不是对应用每条连接身份的保证，既有系统/应用缓存、不同域名共享 IP、应用自行 DoH/DoT 等均会让分流信息不完整或产生歧义。[官方定义](https://github.com/SagerNet/sing-box/blob/v1.14.4/docs/configuration/dns/index.md#reverse_mapping)、[缓存创建及记录源码](https://github.com/SagerNet/sing-box/blob/v1.14.4/dns/router.go#L116)、[记录响应地址](https://github.com/SagerNet/sing-box/blob/v1.14.4/dns/router.go#L1121)。

初始生成器没有 `sniff` 和 FakeIP。对只携带目标 IP 的 TUN 连接，如果反向映射缺失，域名规则不能凭空恢复名称。于是 blacklist 的 `final: direct` 会把无法识别的黑名单站点流量送直连；bypass-cn 的 `final: vpngate` 会把无法识别的大陆流量送 VPN，通常是可用性/分流准确性问题。HTTP CONNECT / SOCKS 请求若直接携带域名，识别条件不同。[最终路由定义](https://github.com/SagerNet/sing-box/blob/v1.14.4/docs/configuration/route/index.md#final)、[域名恢复及匹配源码](https://github.com/SagerNet/sing-box/blob/v1.14.4/route/route.go#L591)。

官方 `sniff` 是非终止路由动作，可提取 HTTP Host、TLS Server Name、QUIC Server Name。把它放在域名分流前能改善预缓存和应用自带解析后的可识别连接，但不保证读取所有加密握手或无域名协议，也不会把硬编码 IP 自动标成某个黑名单域名。其能力应描述为补足域名信息，不应写成绝对防泄漏措施。[sniff 动作](https://github.com/SagerNet/sing-box/blob/v1.14.4/docs/configuration/route/rule_action.md#sniff)、[支持协议](https://github.com/SagerNet/sing-box/blob/v1.14.4/docs/configuration/route/sniff.md)。

FakeIP 是官方支持的另一种域名关联机制，自 1.12 使用 `type: fakeip` DNS server。采用它需要同时处理地址池、缓存、内网及不兼容应用；未进入本内核 DNS 的查询仍不自动受其控制。本次审计不要求仅为消除术语上的“缺失”而引入 FakeIP。[固定版 FakeIP 格式](https://github.com/SagerNet/sing-box/blob/v1.14.4/docs/configuration/dns/server/fakeip.md)。

`hijack-dns` 将匹配的请求交给内核 DNS 模块，不会解密任意 HTTPS/DoT。初始 `port: 53` 规则可接管进入内核的普通 DNS；应用发向 443/853 的加密解析仍作为业务连接按所选预设路由。全局模式中的此类连接在被 TUN 捕获且未命中直连例外时走 VPN；黑名单模式不能承诺所有自带解析的域名都被黑名单识别。[官方 hijack-dns 动作](https://github.com/SagerNet/sing-box/blob/v1.14.4/docs/configuration/route/rule_action.md#hijack-dns)。

## mixed 与 Windows 系统代理的范围

官方 `mixed` 提供 SOCKS4/4a/5 和 HTTP 代理协议；未配置 `users` 时没有认证。本仓库监听 `127.0.0.1`，范围是本机 IPv4 回环；这不等同于只允许本软件使用该入口。系统代理由 GUI 自己管理，没有使用内核的 `set_system_proxy`。[官方 mixed 说明](https://github.com/SagerNet/sing-box/blob/v1.14.4/docs/configuration/inbound/mixed.md)。

GUI 修改当前用户的 Internet Settings 并调用 WinINet 刷新。Microsoft 文档说明 WinINet 的预配置模式读取 `ProxyEnable`、`ProxyServer`、`ProxyOverride`；WinHTTP 要复用这些设置，需要客户端主动导入/设置相应会话，存在独立配置途径。因此只能承诺遵守这些设置的应用会采用本地代理，不能承诺服务、其他账户、直连 Winsock、应用自定义代理自动覆盖。[Microsoft WinINet 配置](https://learn.microsoft.com/en-us/windows/win32/wininet/enabling-internet-functionality)、[Microsoft WinHTTP 复用 WinINet 配置](https://learn.microsoft.com/en-us/windows/win32/winhttp/setting-wininet-proxy-configurations-in-winhttp)。

由此推导：**仅系统代理模式不具备全机 IP/DNS 防泄漏保证**。普通 HTTP 代理设置不会自动接管任意 UDP（例如未遵守代理的 QUIC、STUN/WebRTC）、系统 DNS 或其他直接套接字。应用是否在请求前本地解析也由应用协议和实现决定。HTTP/SOCKS 服务支持什么，与 Windows 自动代理设置实际覆盖什么，必须分别验收。

Microsoft 提供 `INTERNET_PER_CONN_OPTION` 查询/设置自动发现、PAC URL、手工代理及连接标志。初始实现直接写 `AutoDetect` DWORD，不足以仅通过文档推定所有连接的 WPAD 标志都已关闭；应查询有效连接选项验证。此项是待验证 API 一致性问题，不是本轮已复现的泄漏。[Microsoft 连接选项](https://learn.microsoft.com/en-us/windows/win32/api/wininet/ns-wininet-internet_per_conn_optiona)、[设置与查询作用域](https://learn.microsoft.com/en-us/windows/win32/wininet/setting-and-retrieving-internet-options)。

## 审计要求与验证边界

1. 保留生产 TUN 的双栈捕获、自动路由、严格路由和避免回环的出站接口选择。显式 `dns_mode: hijack` 可固定审查意图，不能宣称原默认值没有防护。
2. 移除 `independent_cache`；若不要求固定旧栈，省略 `stack`，并分别检查稳定版与允许安装的最新 Alpha。不能宣称 `stack:gvisor` 消除了 1.15 的整项弃用。
3. DNS 路由与业务路由保持一致；必须记录上游、上游进程、内网名称及预设自身的直连范围。若保留明文直连解析，文案应如实说明。
4. 在域名规则前考虑 `sniff`，检验缓存命中、DoH 后的 TLS/QUIC、共享 IP 和未知 IP 的分流；黑名单默认直连模式不能作为“未知流量也不泄漏真实 IP”的模式。
5. 确认上游进程豁免只指向实际可信外部代理；该进程的全部网络连接可以成为 direct 例外，不能从 UI 的“全局”名称推断它也必须进 VPN。不要扩大系统路由排除去掩盖回环问题。
6. “VPN 分支在内核仍运行时不可用会失败”与“GUI/内核退出后持续阻断物理网络”是两个要求。初始 endpoint 的未就绪返回错误、显式 route/final 没有自动选择 direct，但操作系统级持续断网保护并不存在。[OpenVPN 未就绪行为](https://github.com/SagerNet/sing-box/blob/v1.14.4/protocol/openvpn/client.go#L772)。
7. `sing-box check`、单元测试、回环 OpenVPN 测试仅能证明对应语法和受控链路。真实 Windows 多网卡 DNS、物理接口 IPv4/IPv6、UDP/STUN、DoH、重新连接、系统休眠、上游退出、内核/GUI 崩溃与高优先级路由，需要隔离环境下抓包和验收；本文件不声称这些现场验证已经完成。
