# singbox_vpngate

基于 GUI.for.SingBox 的 Windows 桌面客户端，保留原有主题和控件风格，
使用 VPNGate OpenVPN TCP 节点作为出口，不再使用通用节点订阅。

```
电脑 → sing-box TUN / 系统代理 → OpenVPN 隧道（经外部 mixed 中转）→ VPNGate → 网站
```

- 独立 VPNGate 节点页：去重、默认 200 条容量、自动补充、手动选点。
- 独立实例真连接测速：握手后发送 HTTPS 请求，展示延时、状态和失败原因；支持取消和删除不可用节点。
- 直连、绕过大陆域名、黑名单、全局四种预设；域名规则支持手动／自动更新。
- 管理下载与测速始终经过指定的 HTTP CONNECT / SOCKS4 / SOCKS4a / SOCKS5 上游。
- GUI 直接管理内核，无需安装后台服务；提供按需“修复软件”按钮。
- GUI 不附带 sing-box 或 libcronet；启动时检查内核，在设置中按需下载、更新或切换指定版本。
- 设置保留 VPNGate、内核与外观，移除旧高级配置、插件、计划任务和关于功能。

主连接使用一个 sing-box 进程；测速使用同一可执行文件启动独立进程，
不会改写主连接的配置、TUN 或系统代理。外部 mixed 是 OpenVPN 的上游，
本软件的 mixed 是业务流量入口，两者不能使用同一个监听端口。
链路、故障定位和修正依据见 [链路审计](docs/vpngate-chain-audit.md)。
各协议的解析方式与认证限制见 [mixed 协议说明](docs/mixed-upstream-protocols.md)。

## 构建

需要 Windows amd64、Go（版本见 go.mod）、Node.js 和 pnpm：

```powershell
pnpm --dir frontend install --frozen-lockfile
powershell -File scripts/build-vpngate.ps1
```

初始分流规则由构建脚本按固定的上游提交下载并校验 SHA-256，`.srs` 资源不提交到 Git。
直接运行 Go 命令前，先执行 `powershell -File scripts/prepare-rule-resources.ps1`；
运行整个 Go 项目的测试或编译还需要先构建前端（`pnpm --dir frontend build`）。
规则来源和固定版本见 [资源说明](internal/vpngateruntime/rules/README.md)。

产物在 `build/bin/singbox_vpngate-gui-windows-amd64`，是直接运行的文件夹，
仅包含 `singbox_vpngate.exe`、`GUI-LICENSE.txt`、`ACCEPTANCE.md` 和
`checksums.json`，不打包 ZIP、不附带 sing-box、libcronet 或内核许可证。
原始 GUI 构建文件仍为 `build/bin/singbox_vpngate.desktop.exe`。
sing-box 1.14.4 官方校验包仅保留在 `build/bin/vpngate-cache`，供构建时配置检查
与原生 OpenVPN 回环测试使用。构建脚本不会改变本机网络设置，也不会删除旧产物或用户数据；
若新的分发目录已含其他文件或运行数据，构建会停止打包并保留它们。

首次使用先启动外部 mixed，默认连接为 HTTP `127.0.0.1:10808`，不带认证。
在设置中按实际监听端口、协议及认证信息调整并保存；若正在编辑上游，先保存再查询或下载内核。
启动检测到内核缺失会提示前往“设置 → 内核”：使用原版 Stable / Alpha 面板查看本地、远程版本并下载更新。
内核需为支持原生 OpenVPN、包含 `with_gvisor` 的官方 sing-box 1.14 或更高版本。
版本查询、下载及其他管理请求只经过已保存的外部 mixed。
更新或切换内核前需停止主连接并等待所有任务结束。之后再获取节点、测速、选择节点并手动连接。
启动、下载内核和切换版本均不会自动接管网络。客户端与构建名称仍为 `singbox_vpngate`，
保留既有用户数据路径。
启用 TUN 时需通过界面以管理员身份重启；外部代理自身的 TUN 应关闭。
正常退出软件会停止连接并恢复本软件修改的系统代理；隐藏到托盘则继续运行。
异常后可点击“修复软件”，停止本软件连接、恢复代理并清理运行残留，保留节点和配置。

## 验证与使用边界

测试和构建通过不等同于公网 VPNGate 连接已经验收。软件不提供跨进程崩溃的持续断网保护。
生产使用前需要完成实际连接及退出、异常修复检查。
步骤、恢复命令及自动化验证范围见 [使用与验收说明](docs/vpngate-acceptance.md)。

原项目：[GUI.for.SingBox](https://github.com/GUI-for-Cores/GUI.for.SingBox)。
规则数据来源：[MetaCubeX/meta-rules-dat](https://github.com/MetaCubeX/meta-rules-dat)。
