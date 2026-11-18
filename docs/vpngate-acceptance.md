# VPNGate desktop usage and acceptance

Build: `powershell -File scripts/build-vpngate.ps1`. Output:
`build/bin/singbox_vpngate-gui-windows-amd64`. This unpacked distribution contains
only `singbox_vpngate.exe`, `GUI-LICENSE.txt`, `ACCEPTANCE.md` and `checksums.json`.
It ships no sing-box executable, libcronet DLL or core license; the local build
does not create a ZIP. The raw Wails output remains
`build/bin/singbox_vpngate.desktop.exe`. The GUI manages its own core and runs
without a background service.

1. Start the external HTTP/SOCKS4/SOCKS4a/SOCKS5 mixed upstream with its own TUN disabled.
2. In Settings → VPNGate, configure address, explicit port and optional login.
   Defaults are HTTP `127.0.0.1:10808`, with no authentication. Save using the
   fixed top toolbar. The default pool capacity is 200.
3. A missing-core warning on startup leads to Settings → Core. Query the official
   version list, select the desired version and explicitly download it. Use an
   official sing-box version 1.14 or later with native OpenVPN. Version 1.14 also
   needs `with_gvisor`; 1.15 uses the new native stack without that build tag.
   Version queries and downloads use only the saved external mixed configuration;
   save upstream edits before either operation. Download is an explicit action.
4. Fetch nodes, perform real connectivity tests and choose a node. Fetching and
   testing work without a service or administrator rights. After successful
   connectivity measurements, each isolated candidate tunnel also queries ip-api
   for its observed public IP, country and region. The node list shows these
   measured locations, with the official VPNGate endpoint on the first IP line
   and the queried exit IP on the second. Before a location is measured, the
   country column uses the official feed's country.
5. With a compatible core installed, the GUI starts sing-box automatically.
   Use the original-style Overview toolbar to switch system proxy and TUN
   independently. Both start off on a fresh profile. Existing preferences are
   retained. Windows TUN requires administrator rights. Use Restart as
   administrator if needed and check the effective TUN state in Overview.
6. Choose Direct, Bypass Mainland domains, Blacklist or Global to the right of
   the traffic chart. If the selected node is missing or unreachable, sing-box
   stays running. Traffic entering the core that matches VPN rules fails and
   direct rules still work. Overview shows a compact status and Logs shows
   connection diagnostics. Management downloads and probes always use the
   external mixed upstream.
7. Before updating or switching an installed core, stop it in Overview and
   wait for all jobs to finish. In Settings, download the desired version or
   select an installed version, then use the Overview toolbar to start the core.
8. Normal Exit stops the core and jobs and restores the system proxy owned by
   this application. Hiding to the tray keeps the core and its active switches running.

For first-run acceptance, use a fresh copy of the GUI-only folder: confirm the
missing-core warning, save the working external upstream, download a selected
compatible version from Settings and confirm it starts with both capture switches
off. Relaunch and check that the installed core starts automatically again.
Confirm the missing node shows a compact status and leaves the core running.
Repeat with a changed upstream draft to confirm it must be saved before querying
or downloading. Confirm an update requires the core and jobs to stop and
preserves settings and nodes. Then start the updated core from Overview.

## Portable Windows data

Windows uses `data` beside `singbox_vpngate.exe` by default, independent of the
working directory. `data/vpngate` holds the node pool, rules and generated
runtime files. Core downloads go to `data/vpngate/cores`. WebView2 stores its
profile and cache under `data/.cache/webview2`. A fixed WebView2 runtime can be
placed under `data/WebView2`. Otherwise the application uses Windows' installed
WebView2 runtime. Launch from a writable directory and move the executable and
its `data` directory together.

Compatible profiles from an older data path are copied to the portable
directory on first use. The old files remain available. Old caches and proxy
ownership snapshots stay with the legacy data. To check portability, launch a
disposable package copy from a different working directory, including a path
containing spaces, and confirm new data is written beside that copy's executable.

## Node egress verification

The free [ip-api JSON endpoint](https://ip-api.com/docs/api:json) uses HTTP rather
than HTTPS. Its request travels through the tested node's isolated VPN tunnel,
with no host/direct fallback. It omits a target IP so the provider reports the
request's observed exit rather than geolocating the VPNGate feed's server address.
The existing HTTPS connectivity and latency tests remain separate.

An unavailable, timed-out or rate-limited geolocation provider leaves a working
node available and shows a separate lookup failure. The exit IP and region show
a placeholder until measured, and the country column falls back to the official
feed country until an egress country is available. Use **Retest egress** to test
only visible available nodes with missing or expired exit data.
Successful egress metadata is cached with its original query timestamp for seven
days (168 hours). Retests, connectivity failures and application restarts retain
the cache and do not extend its lifetime. Changes to the effective candidate
profile or upstream route clear the previous measurement. Feed comments, the
core installation path, the latency URL and the routing preset leave it intact.
A failed refresh keeps the last result marked as expired. A legacy record with
no valid timestamp or language marker needs a new query.

The query requests `lang=zh-CN` for country, region and city names; Chinese UI
country labels also use ISO country codes for existing English measurements.
Legacy English regions are replaced by the localized provider response on the
next successful uncached test. The exit row's tooltip shows the query time.

Automatic node refresh defaults to 30 minutes, and rule updates default to 12
hours; saved custom intervals continue to apply. Attempt/completion clocks are
persisted, so reopening does not reset them or cause repeated startup requests.
Existing feed timestamps and actual rule file update times seed clocks for older
data. Manual fetch/update actions run immediately and reset the next automatic
update baseline. A full node pool still receives scheduled feed metadata updates.

During public-network acceptance, test two nodes with distinct known exits and
compare the displayed IP/location with an independent request through each same
exit. Retest a node and restart the application to verify the same unexpired
cached result/timestamp is retained without another provider request. Verify an
entry exactly seven days old is queried again. Restart before and after the
configured automatic update deadlines and confirm only overdue tasks start.
Also verify search by measured IP and Chinese location, provider-failure
details, and readable light/dark layouts at the minimum 600px desktop width.
Automated fake-proxy checks do not establish public VPNGate interoperability or
the accuracy of ip-api's geolocation database.

## On-demand repair

Use Repair application in Settings or the error banner after an abnormal exit
or a software or system proxy failure. It cancels and waits for owned probes, stops
the owned core, restores the saved proxy only when it still belongs to this
application, and removes generated runtime/probe files. It preserves node pool,
selection, settings and valid rules. Start the core from Overview afterward.

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

## IP and DNS leak acceptance

The official-document review and its version-pinned source references are in
[sing-box security reference audit](singbox-official-reference-audit.md).
Configuration validity is only one prerequisite. Use an isolated Windows test
machine or VM with both working IPv4 and IPv6 for the checks below; ordinary
unit/native-loopback tests never change this computer's networking.

For Internet traffic that must use VPNGate, select **Global** and enable **TUN**.
System proxy alone covers applications that use that proxy; it does not take over
all UDP, WebRTC, DNS, WinHTTP or applications with their own network settings.
Direct and the two domain-split presets deliberately allow some direct traffic.
Even Global retains local/private network and external-upstream exceptions.
An upstream executable exemption covers that entire executable, so it must not
be shared with ordinary browsing or untrusted business traffic.

1. Record physical IPv4/IPv6 addresses, DNS servers, route tables and current
   Windows proxy/PAC settings before enabling TUN or system proxy. Then inspect
   the generated `runtime/config.json` in the application's VPNGate data
   directory and compare its policy with the mode displayed in the GUI. This
   file contains private node material and proxy credentials; do not publish it.
2. In Global + TUN, check the observed public IPv4 from a proxy-aware browser and
   a client that explicitly bypasses system proxies, for example
   `curl.exe --noproxy "*" https://api.ipify.org`. Both must use the intended
   VPNGate exit. Test real IPv6 destinations too: unsupported VPN IPv6 must fail,
   rather than return the physical IPv6. A machine without working native IPv6
   cannot validate IPv6 leak prevention.
3. Capture on every physical adapter, not only the TUN. Use unique, uncached
   public DNS names and TCP/UDP port 53, including explicit queries to the LAN
   router and an unrelated public resolver. Ordinary Global business queries
   must travel inside the VPN; identify any direct resolver traffic by process
   and name. Upstream bootstrap, exempt upstream executable and intentional
   direct/split DNS are distinct documented exceptions, not proof that every
   DNS query is protected. Ordinary `localhost`, `.local` and `.lan` queries
   are hosts-only; unknown private names must fail without public DNS traffic.
   Dynamic LAN DNS discovery is not provided by that hosts-only policy. Repeat
   with Ethernet and Wi-Fi active.
4. Test browser secure DNS, HTTP/3/QUIC and WebRTC while capturing physical
   adapters. Port 53 interception does not intercept DoH/DoT; protection of these
   connections depends on traffic routing. For domain-split modes, test cached
   DNS and a new application session, and understand that unknown IP traffic,
   hidden/encrypted hostnames and unmatched domains can still follow Direct in
   Blacklist mode. A DNS leak website alone does not test these paths.
5. Test reconnect, mode/settings changes, upstream restart, sleep/resume and
   switching physical adapters. If applying a new policy fails before takeover,
   the GUI must retain the policy of the old running connection. Inspect both
   displayed policy and actual routes/proxy state after each transition.
6. Interrupt the VPN data path while keeping the core alive. Confirm VPN-matched
   traffic through the core fails, traffic following direct rules still works,
   and Overview shows a small status with details recorded under Logs. Test
   process termination separately. On exit, crash or core replacement, TUN
   routes can disappear and traffic can resume over the physical interface.
7. Confirm normal Stop/Exit restores the application's owned proxy snapshot and
   does not overwrite another application's later proxy change. Repeat failed
   startup and explicit Repair. Keep packet capture separate from screenshots
   that only show a successful connection indicator.

Windows TUN is also subject to the routing limitations documented in the
[official TunnelVision notice](https://sing-box.sagernet.org/manual/misc/tunnelvision/).
Do not describe this application as providing unconditional leak prevention on
untrusted networks or across process crashes. Such a requirement needs a
separately designed and verified system-level fail-closed policy.
