# Desktop runtime and on-demand repair

> Later correction: the GUI-only distribution and runtime-selected downloaded
> cores supersede the earlier bundled-core assumptions below. See
> [Core management](core-management-restoration.md) for the current contract.

User supersedes the earlier mandatory protection service: no installed/auto-start
background service, GUI owns sing-box. Repair is an explicit button for abnormal
state. Preserve all VPNGate/pool/mixed/routing behavior and fixed Settings toolbar.

## Contracts for this change

- Runtime adds `NewDesktop(dataDir, corePath string) (*Manager,error)` while
  retaining `New(dataDir,guard)` solely for old guarded compatibility/tests.
- Desktop starts scheduler without a guard, pins supplied portable core path over
  legacy installed path, and permits connect without service. TUN requires an
  elevated GUI; return a clear error and never mutate networking during dev tests.
- `RuntimeState.requiresAdmin` indicates current TUN privilege requirement.
  `State.repair` has `needed`, `requiresAdmin`, `message` (JSON lower camel case).
  Existing `service` field may remain for compatibility but product UI ignores it.
- RPC `repair` cancels/waits active probe jobs, stops only owned production core,
  restores owned system proxy snapshot, cleans owned runtime/probe leftovers, and
  resets runtime error. Preserve settings, node pool, selected node and valid rules.
  Add a visible success/error message; never claim arbitrary software bugs fixed.
- Desktop Close/normal GUI exit stops owned children and restores proxy. A crash
  cannot promise persistent kill switch; next startup reports repair needed if a
  proxy snapshot or interrupted runtime remains. No new WFP rules are ever armed.
- Windows proxy snapshots use current GUI user SID, not service installation SID.
  Restores remain conditional on current proxy matching the owned endpoint.
- Bridge becomes one local Desktop Manager in `<Env.DataDir>/vpngate`; no normal
  service RPC calls. Migrate readable old service/draft state without deleting it.
  Portable core comes from `<Env.BasePath>/sing-box.exe` (or existing local core).
- RPC `runtime.elevate` explicitly restarts GUI via existing App.RestartApp(true).
  UI offers it for TUN/legacy repair privilege; no automatic elevation during tests.
- Bridge repair additionally removes legacy installed service/owned WFP filters
  only after explicit user Repair AND administrator permission. If elevation is
  needed, return actionable message; UI exposes restart-as-admin. Do not install
  services. Legacy ProgramData remains as recovery backup.
- GUI removes install/recover-service controls and mandatory service gates.
  Overview/Settings/error banner expose "修复软件" / "Repair application" with
  concise scope: stops current connection, restores this application's network
  settings, retains nodes/settings. Existing sticky toolbar remains unchanged.
- Distribution ships GUI + pinned sing-box (+ DLL/licenses), no service binary.
  Keep legacy guardian code only for explicitly requested residual cleanup.

## Verification

Meaningful runtime tests for local mode/no-service state, TUN privilege rejection,
repair preserving data and scoped cleanup, graceful shutdown/proxy ownership;
frontend tests for service-free controls and repair RPC. Existing native loopback
test remains exact generated client config. No live TUN, registry/firewall changes,
service installation/uninstallation or real proxy restore in development tests.
