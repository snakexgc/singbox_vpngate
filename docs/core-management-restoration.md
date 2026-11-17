# Core download and version management

2026-11-17: the user restored sing-box core management after settings simplification.
The unpacked distribution contains the GUI, GUI license, acceptance guide and
checksums only. The build cache keeps a verified core solely for native tests.
Old distributions and existing user profiles are preserved.

Settings has VPNGate, Core and Appearance tabs. A later user correction restores
the original core-update UI from Git HEAD: Stable/Alpha rows, local/remote version
tags, the original update/cancel controls and toolbar, and Stable/Alpha selection
cards. The custom replacement download form is removed. An adapter keeps these
controls on the current managed official-release/install/selection RPCs. It shares
release discovery between both rows, separates branch versions, and tracks the
exact install job through state polling. Core supports local detection, download
progress/cancellation and rollback to an older downloaded branch version. Startup
checks the selected core and shows a fixed warning below the main navigation if
it is unavailable or incompatible; its button opens Core even after changing tabs.
Administrator and missing-core notices are independent. No automatic elevation,
core download, core update or connection is added.

This UI correction does not add runtime launch-option behavior: the restored
legacy environment/argument dialog retains its original frontend persistence,
while VPNGate still owns generated launch arguments. The legacy cache toolbar
action retains its original file target; managed configurations do not currently
enable sing-box cache_file. These ancillary controls are distinct from the
restored update/download/branch-switch behavior.

New profiles use HTTP at 127.0.0.1:10808 with empty username/password. Existing
upstream settings are not overwritten. Release metadata and archive downloads use
the saved external mixed proxy, supporting HTTP/SOCKS4/SOCKS4a/SOCKS5 without an
environment/system/direct fallback. Editing an upstream requires saving it before
starting a release query or download.

The runtime owns version selection. It validates official release metadata and
archive SHA-256 before extracting or executing the candidate, bounds extraction,
and checks reported version, OpenVPN/gVisor support and generated configuration.
Versions install into separate owned directories with their companion DLL/license.
Selection is committed through the existing atomic state file, with rollback if
persistence fails. Failure/cancellation preserves the current selection. Repair
preserves downloaded cores. A core operation requires the connection and other
jobs to stop; new conflicting work cannot begin during installation. Connections,
probes and rules use the same selected path, and switching invalidates old probe
measurements. Automatic node/rule jobs wait for an installed core.

Local core detection does not run on every state poll or animate unrelated Refresh
buttons. Selection changes and terminal core jobs trigger a fresh check; an install
finishing during a previous check queues another check. Disposed polls/checks cannot
update the app.

The [official OpenVPN client documentation](https://sing-box.sagernet.org/configuration/endpoint/openvpn-client/)
dates native support to 1.14.0. The [1.14.4 default build tags](https://raw.githubusercontent.com/SagerNet/sing-box/v1.14.4/release/DEFAULT_BUILD_TAGS)
include OpenVPN and gVisor. Version/download metadata comes from
[official sing-box releases](https://github.com/SagerNet/sing-box/releases).

Verification targets include controlled-proxy release/download tests, actual ZIP
extraction and SHA-256 rejection, cancellation, persistence rollback, exclusive
core selection, version/config validation, frontend state races, and native GUI
inspection using an isolated profile. Public VPNGate/TUN acceptance is separate.

Observed verification: 49 frontend tests passed, including missing-core detection,
quiet state synchronization, queued rechecks after an installation and disposal.
Default/proxy and status-race regressions were observed failing before their fixes.
The full Go race suite and vet passed after fixing scheduler-free test fixtures.
Backend tests cover official-URL HTTPS through a controlled CONNECT proxy, SHA-256,
real ZIP extraction, cancellation, atomic rollback, offline selection and a generated
configuration checked by the cached official executable. Production GUI build and
native isolated-profile inspection passed. The three shipped payload checksums and
the fresh GUI match were verified; the distribution has exactly four GUI-only files.
No user profile download, UAC/elevation, live tunnel or OS network change was tested.

The subsequent original-UI restoration passed 60 frontend tests, including 11
adapter tests for shared release discovery, branch separation, install/cancel,
rollback, exact job ownership and stale-poll progress. Original templates were
compared with HEAD, reviewed independently, and inspected in the native GUI with
an isolated profile and disabled upstream. The GUI-only package was rebuilt and
all payload hashes checked again.
