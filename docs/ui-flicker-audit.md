# UI polling and transient-state audit

2026-11-17: investigated the Overview Refresh button blinking about every three
seconds and audited the other reachable pages for the same behavior.

## Confirmed causes and corrections

1. VPNGate state polling runs every 2500 ms (1000 ms during jobs). It called the
   same refresh function as a manual click, toggling the shared `loading` flag.
   Both Overview Refresh and the error-banner Refresh displayed a short spinner
   every poll. Background polling and state synchronization after operations now
   read silently. Explicit refresh retains feedback, joins an in-flight state
   request and does not issue another RPC. Stale read failures also check the
   mutation revision before showing an error.
2. Shared Button inserted a forbidden icon on disable and inserted/replaced an
   icon on loading. Short operations therefore changed the width of unrelated
   buttons disabled by the shared busy state. Disabled buttons now use opacity;
   loading overlays a spinner while retaining the icon/text footprint. Empty
   spinner-only buttons retain their existing 14/16 px content dimensions.
3. WebSocket reconnect used two untracked timers. Disconnecting after the first
   timer could still open a new socket three seconds later. Disconnect now clears
   one tracked retry timer; generation and current-socket guards reject stale
   callbacks. Explicit reconnect works, duplicate connects are ignored, and the
   previous total four-second retry delay remains.
4. Log rows used their message text as a key, colliding for repeated messages.
   Each received log now gets a distinct, stable local sequence ID. This corrects
   an identity weakness; visible flicker from that collision was not independently
   reproduced.

## Other audited paths

- Overview's connected watcher follows a boolean, so equal snapshots do not
  recreate monitoring connections.
- Node rows and running jobs already use stable IDs. Node filters, sorting and
  selected IDs are not reset on ordinary polling.
- Clean Settings drafts may synchronize, but dirty drafts and Saved/Unsaved state
  are protected. The fixed action toolbar remains in place.
- Routing controls depend on real operations, presets and jobs rather than the
  background state-read flag.
- Traffic-chart updates follow actual telemetry. Manual Appearance, Profiles,
  Plugins and Tasks controls have no periodic loading toggles.

Real state changes and explicitly requested operations still produce visible
feedback. This audit removes synthetic loading/layout changes from background
reads; it does not hide real errors or task progress.

## Verification

The 42 frontend tests cover silent poll ticks, foreground request joining, stale
errors, serialized mutations, 10 WebSocket lifecycle cases, and 8 compiled
Button/Icon rendering and production-style cases. The new failures were observed
before their fixes. Type checking, production build and independent review pass.
An isolated native GUI preview was also checked across several polling cycles
and a manual refresh; the Overview controls and layout remained stable.
No backend networking behavior, system proxy, TUN or user node data is changed.
