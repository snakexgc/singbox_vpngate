# Settings simplification

Later user correction: Core download/update/version selection is retained as a
dedicated third tab. See core-management-restoration.md. The removal of generic
profile/plugin/task/About tools remains in effect.

2026-11-17: the user removed Advanced configuration, Plugins, Tasks and About.
Settings now has only VPNGate and Appearance; the fixed Save/Reset/status toolbar
and the independent content scroll area remain.

The change removes the feature pages and old editor components, About modal and
update actions, plugin custom actions, legacy command entries and resource picker.
The plugin `window.Plugins`/Vue/AsyncFunction SDK is no longer exposed. Old generic
profile/config generators, plugin runtimes, task runners and resource stores are
removed from the frontend, rather than kept behind hidden tabs.

The legacy kernel store is replaced by a small managed-state/telemetry adapter:
it reads the current VPNGate runtime and mixed port and exposes the existing log,
memory, traffic and connection subscriptions. It does not start another core or
generate generic profiles. Legacy rule-edit context actions and restart floating
buttons are removed; logs retain filtering/pause and stable IDs, and connections
retain inspection and closure.

The four managed main routes (Overview, VPNGate, Routing, Settings), all four
mixed upstream protocols, node pool, native configuration generation, independent
probes, repair, appearance/localization, tray and normal exit remain. VPNGate
automatic refresh and rule updates belong to the managed backend and are distinct
from the removed generic scheduled-task feature.

Only source functionality is removed. Existing node/settings files, legacy user
data and network state are not deleted. Old general app-settings fields may still
be read for compatibility; they do not restore the removed pages or executors.
Distributed licenses remain alongside the executable.

Verification includes frontend behavioral tests, three managed monitoring adapter
tests, type checking, production compilation, dependency/entrypoint searches and
independent review. Native UI verification checks that the four entries are gone,
Appearance still opens, and the retained VPNGate toolbar stays fixed.
