import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { registerHooks } from 'node:module'
import { test } from 'node:test'
import * as Pinia from 'pinia'
import ts from 'typescript'
import * as Vue from 'vue'

registerHooks({
  resolve(specifier, context, next) {
    if (specifier.startsWith('@/'))
      return next(new URL(`../src/${specifier.slice(2)}.ts`, import.meta.url).href, context)
    return next(specifier, context)
  },
})
const { VPNGateRequest } = await import('../src/bridge/vpngate.ts')
const { useVPNGateStore } = await import('../src/stores/vpngate.ts')
const { defaultVPNGateSettings, normalizeVPNGateState } = await import('../src/utils/vpngate.ts')

function loadModule(path, dependencies) {
  const { outputText } = ts.transpileModule(readFileSync(new URL(path, import.meta.url), 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  })
  const module = { exports: {} }
  new Function('require', 'module', 'exports', outputText)(
    (name) => {
      assert.ok(name in dependencies, `Unexpected dependency: ${name}`)
      return dependencies[name]
    },
    module,
    module.exports,
  )
  return module.exports
}

function setup(respond = () => undefined) {
  Pinia.setActivePinia(Pinia.createPinia())
  const state = normalizeVPNGateState({ settings: defaultVPNGateSettings(), nodes: [], jobs: [] })
  const calls = []
  globalThis.window = {
    go: {
      bridge: {
        App: {
          VPNGateRequest: async (method, payload) => {
            const value = JSON.parse(payload)
            calls.push([method, value])
            const result = await respond(method, value, state)
            return { flag: true, data: JSON.stringify(result ?? state) }
          },
        },
      },
    },
  }
  const vpn = useVPNGateStore()
  vpn.state = state
  const { useCoreManagerStore } = loadModule('../src/stores/coreManager.ts', {
    pinia: Pinia,
    vue: Vue,
    '@/bridge/vpngate': { VPNGateRequest },
    '@/stores/vpngate': { useVPNGateStore },
  })
  const manager = useCoreManagerStore()
  const settings = Vue.reactive({ app: { kernel: { branch: 'main' } } })
  const errors = []
  const hook = loadModule('../src/hooks/useCoreBranch.ts', {
    vue: Vue,
    '@/bridge': { BrowserOpenURL() {}, OpenDir() {} },
    '@/enums/app': { Branch: { Main: 'main', Alpha: 'alpha' } },
    '@/stores/appSettings': { useAppSettingsStore: () => settings },
    '@/stores/coreManager': { useCoreManagerStore: () => manager },
    '@/stores/vpngate': { useVPNGateStore },
    '@/utils/interaction': {
      message: { error: (error) => errors.push(error), success() {} },
      confirm: async () => undefined,
    },
  })
  return { manager, vpn, state, calls, hook, settings, errors }
}

const job = (overrides = {}) => ({
  id: 'download-alpha',
  kind: 'core',
  status: 'running',
  total: 100,
  completed: 25,
  available: 0,
  failed: 0,
  message: 'Downloading archive',
  startedAt: 'now',
  finishedAt: '',
  ...overrides,
})
const installed = (version, selected = false) => ({
  version,
  path: `${version}/sing-box.exe`,
  selected,
})
const flush = async () => {
  for (let i = 0; i < 12; i++) await Promise.resolve()
}

test('both original branch rows share release discovery and keep stable and alpha versions separate', async () => {
  const env = setup((method) => {
    if (method === 'core.releases')
      return {
        releases: [
          { version: 'v1.15.0-alpha.2', prerelease: true, publishedAt: 'now' },
          { version: 'v1.14.10', prerelease: false, publishedAt: 'before' },
        ],
      }
    if (method === 'core.status')
      return {
        path: 'stable.exe',
        installed: true,
        version: '1.14.9',
        error: '',
        installedVersions: [installed('v1.15.0-alpha.1'), installed('v1.14.9', true)],
      }
  })
  const scope = Vue.effectScope()
  const [stable, alpha] = scope.run(() => [env.hook.useCoreBranch(), env.hook.useCoreBranch(true)])
  await flush()
  assert.equal(stable.localVersion.value, '1.14.9')
  assert.equal(alpha.localVersion.value, '1.15.0-alpha.1')
  assert.equal(stable.remoteVersion.value, '1.14.10')
  assert.equal(alpha.remoteVersion.value, '1.15.0-alpha.2')
  assert.equal(env.calls.filter(([method]) => method === 'core.releases').length, 1)
  assert.equal(env.calls.filter(([method]) => method === 'core.status').length, 1)
  scope.stop()
})

test('install progress and cancellation belong only to the row that submitted the managed job', async () => {
  const env = setup((method, payload, state) => {
    if (method === 'core.install') {
      assert.deepEqual(payload, { version: '1.15.0-alpha.2' })
      state.jobs = [job()]
      return state.jobs[0]
    }
    if (method === 'jobs.cancel') state.jobs[0].status = 'canceled'
  })
  env.vpn.coreStatus = { path: '', version: '', installed: false, error: '' }
  env.manager.releases = [{ version: 'v1.15.0-alpha.2', prerelease: true, publishedAt: '' }]
  // Exercise the managed operation directly so no initial metadata request can affect it.
  assert.equal(await env.manager.installCore(true, '1.15.0-alpha.2'), true)
  assert.equal(env.manager.downloadingBranch, true)
  assert.equal(env.manager.installJob.id, 'download-alpha')
  assert.equal(await env.manager.cancelInstall(), true)
  assert.equal(env.manager.downloadingBranch, undefined)
  assert.deepEqual(
    env.calls.find(([method]) => method === 'jobs.cancel'),
    ['jobs.cancel', { id: 'download-alpha' }],
  )
  assert.equal(
    env.calls.some(([method]) => method === 'connect'),
    false,
  )
})

test('a failed release query stays on the managed route and can be retried explicitly', async () => {
  let attempts = 0
  const env = setup((method) => {
    assert.equal(method, 'core.releases')
    if (++attempts === 1) throw new Error('Saved upstream is unavailable')
    return { releases: [{ version: 'v1.14.10', prerelease: false, publishedAt: '' }] }
  })
  assert.equal(await env.manager.loadReleases(), false)
  assert.match(env.manager.releasesError, /Saved upstream is unavailable/)
  assert.deepEqual(env.manager.releases, [])
  assert.equal(await env.manager.loadReleases(true), true)
  assert.equal(env.manager.releases[0].version, 'v1.14.10')
  assert.equal(env.manager.releasesError, '')
  assert.deepEqual(
    env.calls.map(([method]) => method),
    ['core.releases', 'core.releases'],
  )
})

test('offline branch selection activates the newest installed semantic version before changing preference', async () => {
  let finish
  const env = setup((method, payload, state) => {
    if (method === 'core.use') {
      assert.deepEqual(payload, { version: 'v1.15.0-alpha.10' })
      return new Promise((resolve) => {
        finish = () => resolve(state)
      })
    }
    if (method === 'core.status') return env.vpn.coreStatus
    assert.equal(method, 'state')
  })
  env.vpn.coreStatus = {
    path: 'stable.exe',
    version: '1.14.9',
    installed: true,
    error: '',
    installedVersions: [installed('v1.15.0-alpha.2'), installed('v1.15.0-alpha.10')],
  }
  const choosing = env.hook.chooseCoreBranch(true)
  assert.equal(env.settings.app.kernel.branch, 'main')
  finish()
  assert.equal(await choosing, true)
  assert.equal(env.settings.app.kernel.branch, 'alpha')
  assert.equal(
    env.calls.some(([method]) => ['core.releases', 'core.install', 'connect'].includes(method)),
    false,
  )
})

test('unavailable or rejected branch activation preserves the original selection', async () => {
  const env = setup((method) => {
    if (method === 'core.use') throw new Error('Stop the connection first')
    if (method === 'core.status') return env.vpn.coreStatus
  })
  env.vpn.coreStatus = { path: 'stable.exe', version: '1.14.9', installed: true, error: '' }
  assert.equal(await env.hook.chooseCoreBranch(true), false)
  assert.equal(env.settings.app.kernel.branch, 'main')
  assert.equal(env.errors.length, 1)
  assert.equal(env.calls.length, 0)
  env.vpn.coreStatus.installedVersions = [installed('v1.15.0-alpha.1')]
  assert.equal(await env.hook.chooseCoreBranch(true), false)
  assert.equal(env.settings.app.kernel.branch, 'main')
  assert.match(String(env.errors.at(-1)), /Stop the connection first/)
})

test('rollback selects only an older installed version from its own branch', async () => {
  const env = setup((method, payload) => {
    if (method === 'core.use') assert.deepEqual(payload, { version: 'v1.14.9' })
    if (method === 'core.status') return env.vpn.coreStatus
  })
  env.vpn.coreStatus = {
    path: 'current.exe',
    installed: true,
    version: '1.14.10',
    error: '',
    installedVersions: [
      installed('v1.14.2'),
      installed('v1.14.9'),
      installed('v1.14.10', true),
      installed('v1.15.0-alpha.10'),
    ],
  }
  assert.equal(env.manager.localCore(false).version, 'v1.14.10')
  assert.equal(env.manager.rollbackVersion(false), 'v1.14.9')
  assert.equal(env.manager.rollbackVersion(true), '')
  assert.equal(await env.manager.rollbackCore(false), true)
  assert.equal(
    env.calls.some(([method]) => method === 'core.install'),
    false,
  )
})

test('a bundled compatible core remains visible only in its own branch without managed installs', () => {
  const env = setup()
  env.vpn.coreStatus = { path: 'bundled.exe', version: '1.14.10', installed: true, error: '' }
  assert.equal(env.manager.localCore(false).version, '1.14.10')
  assert.equal(env.manager.localCore(true), undefined)
  assert.equal(env.manager.rollbackVersion(false), '')
})

test('a later core operation cannot replace the install job shown in its original branch', async () => {
  const env = setup((method, payload, state) => {
    if (method === 'core.install') {
      state.jobs = [job()]
      return state.jobs[0]
    }
  })
  await env.manager.installCore(true, '1.15.0-alpha.2')
  env.vpn.state.jobs[0].status = 'completed'
  env.vpn.state.jobs = [job({ id: 'later-core-switch' }), ...env.vpn.state.jobs]
  assert.equal(env.manager.installJob.id, 'download-alpha')
  assert.equal(env.manager.downloadingBranch, undefined)
})

test('accepted installs keep their branch pending while an obsolete state read hides the new job', async () => {
  let finishOldRead
  let remote
  let reads = 0
  const env = setup((method) => {
    if (method === 'state' && ++reads === 1)
      return new Promise((resolve) => {
        finishOldRead = resolve
      })
    if (method === 'state') return remote
    if (method === 'core.install') {
      remote.jobs = [job()]
      return remote.jobs[0]
    }
  })
  remote = structuredClone(env.state)
  const obsolete = structuredClone(remote)
  const reading = env.vpn.refresh()
  assert.equal(await env.manager.installCore(true, '1.15.0-alpha.2'), true)
  assert.equal(env.manager.installJob, undefined)
  assert.equal(env.manager.downloadingBranch, true)
  assert.equal(await env.manager.installCore(false, '1.14.10'), false)
  finishOldRead(obsolete)
  await reading
  await env.vpn.refresh()
  assert.equal(env.manager.installJob.id, 'download-alpha')
  assert.equal(env.manager.downloadingBranch, true)
  assert.equal(env.calls.filter(([method]) => method === 'core.install').length, 1)
})

test('original update rows show percentage progress only on the downloading branch', async () => {
  const env = setup((method, payload, state) => {
    if (method === 'core.releases')
      return {
        releases: [{ version: 'v1.15.0-alpha.2', prerelease: true, publishedAt: '' }],
      }
    if (method === 'core.status')
      return {
        path: 'stable.exe',
        installed: true,
        version: '1.14.10',
        error: '',
      }
    if (method === 'core.install') {
      state.jobs = [job()]
      return state.jobs[0]
    }
  })
  const scope = Vue.effectScope()
  const [stable, alpha] = scope.run(() => [env.hook.useCoreBranch(), env.hook.useCoreBranch(true)])
  await flush()
  await alpha.downloadCore()
  assert.equal(stable.downloading.value, false)
  assert.equal(alpha.downloading.value, true)
  assert.match(alpha.downloadProgress.value, /25\.00%/)
  assert.equal(typeof alpha.cancelDownload.value, 'function')
  assert.equal(stable.cancelDownload.value, undefined)
  assert.equal(stable.restartable.value, false)
  assert.equal(alpha.restartable.value, false)
  scope.stop()
})

test('reopening the original updater refreshes shared release metadata once for both rows', async () => {
  let version = 'v1.14.10'
  const env = setup((method) => {
    if (method === 'core.releases')
      return {
        releases: [{ version, prerelease: false, publishedAt: '' }],
      }
    if (method === 'core.status')
      return {
        path: '',
        installed: false,
        version: '',
        error: '',
      }
  })
  const first = Vue.effectScope()
  first.run(() => [env.hook.useCoreBranch(), env.hook.useCoreBranch(true)])
  await flush()
  first.stop()
  version = 'v1.14.11'
  const reopened = Vue.effectScope()
  const stable = reopened.run(() => {
    const row = env.hook.useCoreBranch()
    env.hook.useCoreBranch(true)
    return row
  })
  await flush()
  assert.equal(stable.remoteVersion.value, '1.14.11')
  assert.equal(env.calls.filter(([method]) => method === 'core.releases').length, 2)
  reopened.stop()
})
