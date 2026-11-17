# VPNGate chain audit and corrective scope

The user confirmed that the production sing-box must keep running while node
tests use independent processes of the same sing-box executable. No protection
service is required. This audit keeps that process model and fixes the test
orchestration rather than introducing another VPN implementation.

## Intended and actual data path

```text
Application
  -> production sing-box TUN or local mixed inbound
  -> business routing
  -> native OpenVPN endpoint (detour: upstream)
  -> external mixed HTTP/SOCKS4/SOCKS4a/SOCKS5 proxy
  -> selected VPNGate OpenVPN TCP server
  -> destination service
```

The configured external upstream port and this application's local mixed listener
are different ports with different roles. The external upstream must not point
back at this application's mixed listener. TUN excludes the external proxy's own
network process so its physical outbound sockets cannot loop back into TUN.

`internal/vpngate/config.go:BuildConfig` already emits this chain: the endpoint
uses `type: openvpn-client`, `system: false`, `tag: vpngate`, and
`detour: upstream`; upstream is an HTTP or SOCKS4/4a/5 outbound. Native sing-box owns
OpenVPN negotiation, TLS, tunnel packets, DNS forwarding and business routing.
The GUI does not implement an OpenVPN packet relay.

Selected configuration shape (illustrative, certificate material omitted):

```json
{
  "endpoints": [{
    "type": "openvpn-client",
    "tag": "vpngate",
    "system": false,
    "server": "VPNGATE_SERVER_IP",
    "server_port": 443,
    "network": "tcp",
    "detour": "upstream"
  }],
  "outbounds": [{
    "type": "socks",
    "tag": "upstream",
    "version": "5",
    "server": "127.0.0.1",
    "server_port": 10808
  }]
}
```

This fragment is not a complete executable configuration. Actual generation
includes the node's TLS material, DNS, inbounds, business rules and loop exclusions.
Global sends business traffic through VPN; direct/bypass/blacklist intentionally
apply their respective exceptions.

## Management and testing

- Feed and rule downloads use an explicit external proxy directly, so downloading
  a node list does not require an already working VPNGate node. These are ordinary
  management HTTP requests, not a separate VPN transport implementation.
- Each test process receives one candidate endpoint, an independent loopback
  mixed port and a forced VPN route. It does not enable TUN or system proxy and
  does not rewrite or restart the production process.
- The GUI measures actual HTTP(S) requests through that test core. Listening on a
  port alone is not reported as a successful VPN connection.
- GUI duties remain node persistence/selection, deterministic config generation,
  process lifecycle, result presentation and recovery of its own proxy state.

## Confirmed failures in the previous orchestration

1. `network.go:preflight` hard-coded a HEAD request to the VPNGate homepage.
   `probeBatch` repeated that request after a failed node and canceled the whole
   batch when the homepage timed out. Homepage TLS availability does not determine
   whether the mixed proxy can connect to an OpenVPN node. The screenshot's 8/85
   progress is consistent with that recheck, but does not itself identify which
   node originally failed.
2. The configured test timeout was 45 seconds by default, but the readiness stage
   had an additional hidden 20-second cutoff. Slow tunnels could fail early.
3. Readiness errors discarded the last HTTP failure. Generated probe directories
   were removed on completion, losing the core's most useful error evidence.
4. Listener reachability alone cannot validate proxy credentials. Native 1.14.4
   can report only `endpoint is not ready yet` after upstream authentication is
   rejected, so guessing authentication failures from core log keywords is not
   reliable. A local native regression reproduced the incorrect failed/node
   result for wrong SOCKS5 credentials; proxy-protocol checks avoid that verdict.

The deterministic local reproduction returned HTTP 204 through a proxy while
stalling only the homepage TLS connection. The previous code then produced the
exact screenshot error, including `TLS handshake timeout`. Regression tests also
showed an invalid node stopping an otherwise independent two-node batch at 1/2.

The correction removes website availability from node-test gates. Listener and
standard proxy-protocol checks diagnose only the configured upstream. SOCKS5
greeting/authentication needs no external target; HTTP authentication checks use
CONNECT to the actual candidate's OpenVPN address and transfer no VPN data.
Target-specific CONNECT failures must not be generalized into a shared outage.
Actual listener/proxy-authentication failure remains an environment failure and
preserves existing node health. The sing-box process still performs the actual
OpenVPN negotiation and data forwarding. Readiness shares the configured per-node
budget, and bounded, credential-redacted core diagnostics are copied into the
result before temporary files are cleaned.

## Verification and limits

Read-only checks of the user's configured SOCKS5 mixed port succeeded for the
VPNGate API (HTTP 200) and the test URL (HTTP 204). A single homepage HEAD returned
302 without following redirects. This establishes current HTTPS/API relay
availability; the original timeout may still have been transient or specific to
the website/redirect path. No public VPNGate connection or TUN was started.

Native loopback verification uses the exact generated client configuration,
mutual TLS, a local OpenVPN server and a local HTTPS target. Public VPNGate
compatibility and actual Windows TUN capture remain separate real-system checks.
No automated test installs a service or changes system proxy, DNS or firewall.

The expanded native test covers HTTP CONNECT, SOCKS4, SOCKS4a, SOCKS5 and authenticated SOCKS5.
Each case also resolves a `.invalid` hostname using DNS reached through the VPN,
then retrieves HTTPS from that hostname. The client configuration is unchanged;
only the local server maps the reserved target and DNS service to loopback test
listeners. Counters verify an upstream session, VPN DNS requests and one target
HTTPS request for every protocol case.

All default Xray mixed protocols are now implemented, including management
downloads; see [mixed protocol details](mixed-upstream-protocols.md) for DNS and
authentication limits. Independent probes continue to use their own process and
configuration without changing the production instance.

Primary references:

- [Pinned 1.14.4 OpenVPN client configuration](https://github.com/SagerNet/sing-box/blob/v1.14.4/docs/configuration/endpoint/openvpn-client.md)
- [Dial detour fields](https://sing-box.sagernet.org/configuration/shared/dial/#detour)
- [Pinned 1.14.4 mixed inbound](https://github.com/SagerNet/sing-box/blob/v1.14.4/docs/configuration/inbound/mixed.md)
