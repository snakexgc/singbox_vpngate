# singbox_vpngate

基于 GUI.for.SingBox 的 Windows 桌面客户端，保留原有主题和控件风格，
使用 VPNGate OpenVPN TCP 节点作为出口，不再使用通用节点订阅。

```
电脑 → sing-box TUN / 系统代理 → OpenVPN 隧道（经外部 mixed 中转）→ VPNGate → 网站
```

- 独立 VPNGate 节点页：去重、默认 200 条容量、自动补充、手动选点。
- 独立实例真连接测速：握手后发送 HTTPS 请求，展示延时、状态和失败原因；同时经被测 VPNGate 出口查询真实 IP、中文国家和地区，成功结果缓存 7 天，支持取消和删除不可用节点。
- 节点列表上行显示 VPNGate 官方地址，下行显示查询到的出口 IP。取得出口位置前显示官方国家，取得后显示实测国家和地区。节点默认每 30 分钟、规则默认每 12 小时更新，刷新计时保存在本地，重启后继续生效。
- 直连、绕过大陆域名、黑名单、全局四种预设；域名规则支持手动／自动更新。
- 概览沿用原版 GUI.for.SingBox 界面。工具栏可独立切换系统代理和 TUN，四种路由模式位于流量图右侧。规则状态、更新与来源在设置中管理。
- 日志页汇集应用日志、sing-box 日志、运行详情和任务记录。sing-box 日志等级保存后在下次启动内核时生效。
- 管理下载与测速使用设置中的 HTTP CONNECT / SOCKS4 / SOCKS4a / SOCKS5 上游，支持本机和远程地址。本机上游的监听程序由桌面版自动识别，远程上游只需地址、端口和可选认证。
- GUI 直接管理内核，无需安装后台服务；可在设置的「维护与修复」中恢复本应用修改的网络设置。
- 启动软件时自动启动已安装的兼容内核。GUI 分发包仅包含应用，可在设置中下载、更新或切换内核。
- 设置保留 VPNGate、内核与外观，移除旧高级配置、插件、计划任务和关于功能。

主连接使用一个 sing-box 进程；测速使用同一可执行文件启动独立进程，
不会改写主连接的配置、TUN 或系统代理。外部 mixed 是 OpenVPN 的上游，
本软件的 mixed 是业务流量入口。上游也在本机时，请使用不同的监听端口。
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

应用发布号采用 `vYYYYMMDDN`，例如 `v202611181`，其中 `N` 是当天的发布序号。
更新 `VERSION` 和 `wails.json` 中对应的 Windows 数值版本（例如 `2026.1118.1`），并将同名标签推送到 GitHub。应用界面与 Release 显示 `VERSION` 中的版本号，sing-box 内核版本独立管理。

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
内核需为支持原生 OpenVPN 的官方 sing-box 1.14 或更高版本；1.14 还需包含
`with_gvisor`，1.15 起使用新的原生网络栈，不再以该标签判断兼容性。
版本查询、下载及其他管理请求只经过已保存的外部 mixed。
首次安装兼容内核后即可自动启动。全新配置的 TUN 和系统代理默认关闭，已有开关设置继续保留。
从概览按需打开两项开关。Windows TUN 需管理员权限，外部代理自身的 TUN 应保持关闭。
获取、测试并选择节点后，内核按当前路由模式处理流量。节点未选择或暂不可用时，
内核继续运行，进入本应用且命中 VPN 规则的流量会失败，直连规则继续生效。
概览以简短提示显示节点状态，具体原因可在「日志」查看。

Windows 版的应用数据默认保存在 `singbox_vpngate.exe` 同级的 `data` 目录，
包括应用配置、节点、规则、下载的内核及 WebView2 配置和缓存。使用可写的目录运行，
移动便携版时请将整个文件夹一并移动。兼容的旧配置可以导入，原文件保留。
系统安装的 WebView2 可直接使用，也可在 `data/WebView2` 放置固定版本供此程序使用。

更新或切换内核版本前需在概览停止内核并等待任务结束。切换完成后可在概览启动内核。
正常退出会停止内核和任务并恢复本软件修改的系统代理。隐藏到托盘会继续运行。
内核或网络设置异常时可使用「修复软件」恢复本应用的网络设置并保留节点和配置。

## 验证与使用边界

测试和构建通过不等同于公网 VPNGate 连接已经验收。软件不提供跨进程崩溃的持续断网保护。
生产使用前需要完成实际连接及退出、异常修复检查。
步骤、恢复命令及自动化验证范围见 [使用与验收说明](docs/vpngate-acceptance.md)。

原项目：[GUI.for.SingBox](https://github.com/GUI-for-Cores/GUI.for.SingBox)。
规则数据来源：[MetaCubeX/meta-rules-dat](https://github.com/MetaCubeX/meta-rules-dat)。
