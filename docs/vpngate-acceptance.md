# VPNGate desktop usage and acceptance

Build: `powershell -File scripts/build-vpngate.ps1`. Output:
`build/bin/singbox_vpngate-gui-windows-amd64`. This unpacked distribution contains
only `singbox_vpngate.exe`, `GUI-LICENSE.txt`, `ACCEPTANCE.md` and `checksums.json`.
It ships no sing-box executable, libcronet DLL or core license; the local build
does not create a ZIP. The raw Wails output remains
`build/bin/singbox_vpngate.desktop.exe`. The desktop version needs no installed
service. Its application/build name and existing user-data paths remain
`singbox_vpngate`.

1. Start the external HTTP/SOCKS4/SOCKS4a/SOCKS5 mixed upstream with its own TUN disabled.
2. In Settings → VPNGate, configure address, explicit port and optional login.
   Defaults are HTTP `127.0.0.1:10808`, with no authentication. Save using the
   fixed top toolbar. The default pool capacity is 200.
3. A missing-core warning on startup leads to Settings → Core. Query the official
   version list, select the desired version and explicitly download it. Use an
   official sing-box version 1.14 or later with native OpenVPN and `with_gvisor`.
   Version queries and downloads use only the saved external mixed configuration;
   save upstream edits before either operation. No core downloads occur on startup.
4. Fetch nodes, perform real connectivity tests and choose a node. Fetching and
   testing work without a service or administrator rights.
5. Connect from Overview. TUN requires Windows administrator rights: use the
   explicit Restart as administrator button, then click Connect again. Restart
   never automatically connects or installs a service.
6. Direct, Bypass Mainland domains, Blacklist and Global control business routing.
   Management downloads and probes always use the external mixed upstream.
7. Before updating or switching the core in Settings, stop the main connection
   and wait for all jobs to finish. Select the desired official version and
   explicitly download/update or select an installed version. Startup, download
   and version switching do not automatically connect or take over networking.
8. Normal Exit stops this GUI's core/jobs and restores its owned system proxy.
   Hiding to the tray keeps the process and connection running.

For first-run acceptance, use a fresh copy of the GUI-only folder: confirm the
missing-core warning, save the working external upstream, download a selected
compatible version from Settings and then connect manually. Repeat with a changed
upstream draft to confirm it must be saved before querying/downloading. Confirm
updates/switches require the main connection and jobs to stop, preserve settings
and nodes, and leave the network disconnected until the next explicit Connect.

## On-demand repair

Use Repair application in Overview, Settings or the error banner after an
abnormal exit/connection problem. It cancels and waits for owned probes, stops
the owned core, restores the saved proxy only when it still belongs to this
application, and removes generated runtime/probe files. It preserves node pool,
selection, settings and valid rules. Reconnect manually afterward.

Old service/draft data is imported when applicable and original files remain as
backups. If an older protection service or owned filtering rules remain, explicit
administrator Repair removes those legacy components; it never installs or
restarts the service. If rights are required, restart as administrator first.

The desktop version does not provide a persistent kill switch across GUI/core
crashes. While the core is alive, VPN branches retain their explicit routing and
do not select a direct fallback. On a crash, the next launch can offer repair for
remaining proxy/runtime state. Repair handles this application's network state;
it does not promise to correct arbitrary software or upstream-provider failures.

## Automated verification

The builder retains the checksum-verified official sing-box 1.14.4 archive only in
`build/bin/vpngate-cache`, and supplies that cached executable to configuration and
native loopback tests. It does not copy the core, DLL or core license into the GUI
distribution. Older bundled output directories remain untouched. Packaging refuses
extra files or user data in the new output directory rather than deleting them.
Use `-SkipGui` to run the builder's Go/native-core tests without producing a GUI
distribution. CI may archive the manifest-listed GUI files separately.

```powershell
$env:VPNGATE_CHECK_CORE = 'C:\path\to\sing-box.exe'
$env:VPNGATE_CHECK_RULES = 'C:\path\containing\cn.srs-and-gfw.srs'
$env:VPNGATE_LOOPBACK_CORE = $env:VPNGATE_CHECK_CORE
go test -race ./...
```

The opt-in native loopback test establishes mutual TLS through restricted local
HTTP CONNECT, SOCKS4, SOCKS4a, SOCKS5 and authenticated SOCKS5 proxies, then resolves DNS and sends
HTTPS through OpenVPN to local servers. A rejected SOCKS5 authentication test
checks that invalid upstream credentials are not attributed to the VPNGate node.
Neither endpoint creates a system TUN. Unit tests use isolated files and fake proxy-write
boundaries for repair, shutdown, privilege and retry behavior; they do not change
the registry, firewall, installed services or system proxy.

SOCKS4 uses local IPv4 DNS for management requests; SOCKS4a preserves hostnames for
the proxy. Both use a User ID field rather than password authentication. Xray
rejects SOCKS4/4a when inbound password authentication is enabled. Choose HTTP or
SOCKS5 for an authenticated mixed listener. Switching protocols retains old
passwords until the user explicitly clears them; unsupported passwords block save.

Public VPNGate interoperability, actual Windows TUN takeover, proxy restoration
and legacy cleanup still need isolated real-system acceptance. Building and
running tests do not silently perform these operations on the developer machine.
