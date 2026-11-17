# v2rayN / Xray mixed 上游协议

2026-11-17 核对官方文档、v2rayN 配置生成器与 Xray 源码后，默认本地
mixed 监听支持四种连接协议。本项目均已实现，包括界面、配置生成、节点列表和
规则下载、代理控制检查及独立测速进程。

| 协议 | sing-box 上游配置 | 域名解析与认证 |
| --- | --- | --- |
| HTTP | `type: http` | 普通 HTTP 请求、CONNECT 隧道；支持 HTTPS 目标，可使用代理用户名密码 |
| SOCKS4 | `type: socks, version: 4` | 只支持 IPv4，管理下载在本机解析目标域名；支持 User ID 字段，不支持密码 |
| SOCKS4a | `type: socks, version: 4a` | 管理下载将目标域名交给代理；支持 User ID 字段，不支持密码 |
| SOCKS5 | `type: socks, version: 5` | 支持代理端解析域名及可选用户名密码认证 |

SOCKS4 的本地解析依赖当前本机 DNS；不会为了连接失败而绕过代理直连目标。
原生 OpenVPN 的域名服务器地址仍按生成配置通过显式上游进行 bootstrap DNS。

Xray 会读取并忽略 SOCKS4/4a 的 User ID，不能将它当作登录认证。Xray mixed
启用密码认证时会拒绝 SOCKS4/4a；此时应选择 SOCKS5 或 HTTP。切换协议不会
自动删除已填写的密码：界面保留原值、提示限制，并提供显式清除操作。

HTTP 代理可以通过 CONNECT 访问 HTTPS 网站，但默认 mixed 不是 TLS 加密的
HTTPS 代理监听器。`SOCKS5h` 是 curl 的代理端解析模式名称，不是第五种线协议；
sing-box 的 SOCKS5 配置已经支持传递域名。OpenVPN 使用 TCP，上游 SOCKS5 的
UDP ASSOCIATE 不需要增加为另一项协议。

SOCKS4 拒绝码 91/92/93 不能可靠区分目标拒绝和上游策略拒绝，因此不会据此取消
整池测试。明确的协议错误才归为环境故障，保持此前独立测速及主连接隔离。

## 验证

- 实际 `127.0.0.1:10808` 的监听进程为 Xray。通过该端口分别使用 HTTP、SOCKS4、
  SOCKS4a、SOCKS5 访问 HTTPS 测试地址，四项均返回 HTTP 204。
- 新增管理连接回归覆盖 SOCKS4/4a 帧、User ID、DNS 模式、拒绝响应、取消与套接字
  清理，禁止直连回退。
- 原样生成的 native sing-box 配置完成 HTTP、SOCKS4、SOCKS4a、SOCKS5 和认证
  SOCKS5 的本地 OpenVPN 双向 TLS、VPN 内 DNS 和 HTTPS 测试。SOCKS4a 用仅在
  本地代理后的 DNS 服务中存在的 OpenVPN 主机名，验证显式上游解析路径。
- 开发验证未开启系统 TUN、修改系统代理或连接公共 VPNGate 节点。

## 一手资料

- [Xray SOCKS 入站说明](https://xtls.github.io/en/config/inbounds/socks.html)
- [v2rayN mixed 配置生成](https://github.com/2dust/v2rayN/blob/f57b1bf47961f0d39c690887cf9a2e6941fb1276/v2rayN/ServiceLib/Services/CoreConfig/V2ray/V2rayInboundService.cs#L136-L151)
- [Xray SOCKS4/4a 与密码限制](https://github.com/XTLS/Xray-core/blob/f781c68715cda8a896dde7a19966980a15e53071/proxy/socks/protocol.go#L49-L91)
- [固定版本 sing-box 的 SOCKS 出站](https://github.com/SagerNet/sing-box/blob/v1.14.4/docs/configuration/outbound/socks.md)
- [curl SOCKS5 代理端域名解析](https://curl.se/docs/manpage.html#--socks5-hostname)
