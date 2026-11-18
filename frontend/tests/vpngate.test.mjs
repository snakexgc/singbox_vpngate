import assert from 'node:assert/strict'
import { registerHooks } from 'node:module'
import { test } from 'node:test'
import { createPinia, setActivePinia } from 'pinia'
import { watch } from 'vue'

registerHooks({
  resolve(specifier, context, next) {
    if (specifier.startsWith('@/'))
      return next(new URL(`../src/${specifier.slice(2)}.ts`, import.meta.url).href, context)
    return next(specifier, context)
  },
})
const {
  defaultVPNGateSettings,
  normalizeVPNGateState,
  validateVPNGateSettings,
  filterVPNGateNodes,
  formatVPNGateRegion,
  formatVPNGateEndpoint,
  formatVPNGateCountry,
  submitVPNGateSettingsDraft,
  canConnectVPNGate,
} = await import('../src/utils/vpngate.ts')
const { getVPNGateText } = await import('../src/utils/vpngateLabels.ts')
const { VPNGateRequest } = await import('../src/bridge/vpngate.ts')
const { useVPNGateStore } = await import('../src/stores/vpngate.ts')
const state = () =>
  normalizeVPNGateState({
    settings: defaultVPNGateSettings(),
    nodes: null,
    jobs: null,
  })
const bridge = (fn) => {
  globalThis.window = { go: { bridge: { App: { VPNGateRequest: fn } } } }
}
const flush = async () => {
  for (let i = 0; i < 8; i++) await Promise.resolve()
}

test('first-run defaults use the requested local HTTP mixed proxy without credentials', () => {
  const value = state()
  assert.equal(value.settings.upstream.host, '127.0.0.1')
  assert.equal(value.settings.upstream.port, 10808)
  assert.equal(value.settings.upstream.protocol, 'http')
  assert.equal(value.settings.upstream.username, '')
  assert.equal(value.settings.upstream.password, '')
  assert.equal(value.settings.mixedPort, 20122)
  assert.equal(value.settings.tunEnabled, false)
  assert.equal(value.settings.systemProxyEnabled, false)
  assert.equal(value.settings.poolCapacity, 200)
  assert.equal(value.settings.refreshMinutes, 30)
  assert.equal(value.settings.rulesUpdateHours, 12)
  assert.equal(value.service.installed, false)
  assert.equal(value.runtime.protected, false)
  assert.equal(value.runtime.requiresAdmin, false)
  assert.deepEqual(value.repair, {
    needed: false,
    requiresAdmin: false,
    message: '',
  })
  assert.deepEqual(value.nodes, [])
  assert.equal(validateVPNGateSettings(value.settings), undefined)
})

test('missing core detection stays separate from VPN state and manual refresh loading', async () => {
  setActivePinia(createPinia())
  bridge(async (method) => {
    assert.equal(method, 'core.status')
    return {
      flag: true,
      data: JSON.stringify({ path: 'missing.exe', version: '', installed: false, error: '' }),
    }
  })
  const store = useVPNGateStore()
  await store.refreshCore?.()
  assert.equal(store.coreStatus?.installed, false)
  assert.equal(store.coreStatus?.path, 'missing.exe')
  assert.equal(store.loading, false)
  assert.equal(store.error, '')
})

test('core monitoring rechecks after a completed install, without launching a core check on every poll', async () => {
  setActivePinia(createPinia())
  const value = state()
  value.settings.corePath = 'core-v1.exe'
  let coreChecks = 0
  let installed = false
  bridge(async (method) => {
    if (method === 'core.status') {
      coreChecks++
      return {
        flag: true,
        data: JSON.stringify({
          path: value.settings.corePath,
          installed,
          version: installed ? '1.14.4' : '',
          error: '',
        }),
      }
    }
    assert.equal(method, 'state')
    return { flag: true, data: JSON.stringify(value) }
  })
  const store = useVPNGateStore()
  await store.refresh()
  await store.startCoreMonitoring?.()
  assert.equal(coreChecks, 1)
  await store.refresh()
  await store.refresh()
  assert.equal(coreChecks, 1)
  installed = true
  value.settings.corePath = 'core-v2.exe'
  value.jobs = [{ id: 'install', kind: 'core', status: 'completed', finishedAt: 'finished' }]
  await store.refresh()
  await flush()
  assert.equal(coreChecks, 2)
  assert.equal(store.coreStatus.installed, true)
  assert.equal(store.coreStatus.path, 'core-v2.exe')
})

test('late core detection cannot update the app after polling is disposed', async () => {
  setActivePinia(createPinia())
  let finish
  bridge(
    () =>
      new Promise((resolve) => {
        finish = resolve
      }),
  )
  const store = useVPNGateStore()
  const pending = store.refreshCore?.()
  assert.equal(typeof finish, 'function')
  store.stopPolling()
  finish({
    flag: true,
    data: JSON.stringify({ path: 'late.exe', installed: true, version: '1.14.4', error: '' }),
  })
  await pending
  assert.equal(store.coreStatus, undefined)
  assert.equal(store.coreStatusLoading, false)
})

test('an install completing during an older core check queues a fresh detection', async () => {
  setActivePinia(createPinia())
  const value = state()
  value.settings.corePath = 'old.exe'
  let checks = 0
  let finish
  bridge(async (method) => {
    if (method === 'state') return { flag: true, data: JSON.stringify(value) }
    assert.equal(method, 'core.status')
    checks++
    if (checks === 1)
      return new Promise((resolve) => {
        finish = resolve
      })
    return {
      flag: true,
      data: JSON.stringify({ path: 'new.exe', installed: true, version: '1.14.4', error: '' }),
    }
  })
  const store = useVPNGateStore()
  await store.refresh()
  const pending = store.startCoreMonitoring()
  value.settings.corePath = 'new.exe'
  value.jobs = [{ id: 'install', kind: 'core', status: 'completed', finishedAt: 'now' }]
  await store.refresh()
  finish({
    flag: true,
    data: JSON.stringify({ path: 'old.exe', installed: false, version: '', error: '' }),
  })
  await pending
  await flush()
  assert.equal(checks, 2)
  assert.equal(store.coreStatus.installed, true)
  assert.equal(store.coreStatus.path, 'new.exe')
})

test('desktop core can start without a node or elevated capture permissions', () => {
  const value = state()
  value.settings.upstream.port = 1080
  value.selectedNodeId = 'node'
  value.nodes = [{ id: 'node', status: 'available' }]
  assert.equal(value.service.running, false)
  assert.equal(canConnectVPNGate(value), true)
  value.runtime.requiresAdmin = true
  assert.equal(canConnectVPNGate(value), true)
  value.runtime.requiresAdmin = false
  value.selectedNodeId = ''
  assert.equal(canConnectVPNGate(value), true)
  value.settings.routingPreset = 'direct'
  assert.equal(canConnectVPNGate(value), true)
  assert.equal(canConnectVPNGate(value, 'repair'), false)
  value.jobs = [{ id: 'core-download', kind: 'core', status: 'running' }]
  assert.equal(canConnectVPNGate(value), false)
  value.jobs = []
  value.runtime.status = 'connected'
  assert.equal(canConnectVPNGate(value), false)
  value.runtime.status = 'offline'
  assert.equal(canConnectVPNGate(value), false)
  value.runtime.status = 'starting'
  assert.equal(canConnectVPNGate(value), false)
})

test('repair sends an explicit empty payload and preserves returned nodes/settings', async () => {
  setActivePinia(createPinia())
  const repaired = state()
  repaired.settings.upstream.port = 1080
  repaired.selectedNodeId = 'retained'
  repaired.nodes = [{ id: 'retained', status: 'available' }]
  repaired.repair.message = 'This application network settings were restored.'
  const calls = []
  bridge(async (method, payload) => {
    calls.push([method, JSON.parse(payload)])
    return { flag: true, data: JSON.stringify(repaired) }
  })
  const store = useVPNGateStore()
  assert.equal(await store.run('repair', undefined), true)
  assert.deepEqual(calls, [
    ['repair', {}],
    ['state', {}],
  ])
  assert.equal(store.state.selectedNodeId, 'retained')
  assert.equal(store.state.nodes.length, 1)
  assert.equal(store.state.settings.upstream.port, 1080)
  assert.equal(store.notice, repaired.repair.message)
})

test('administrator restart is explicit and preserves its dedicated RPC response', async () => {
  const calls = []
  bridge(async (method, payload) => {
    calls.push([method, JSON.parse(payload)])
    return { flag: true, data: JSON.stringify({ restarting: true }) }
  })
  assert.deepEqual(await VPNGateRequest('runtime.elevate', undefined), {
    restarting: true,
  })
  assert.deepEqual(calls, [['runtime.elevate', {}]])
})

test('failed legacy cleanup keeps its actionable error and exposes refreshed elevation state', async () => {
  setActivePinia(createPinia())
  const legacy = state()
  legacy.repair = {
    needed: true,
    requiresAdmin: true,
    message: 'Restart as administrator to remove legacy state.',
  }
  const calls = []
  bridge(async (method) => {
    calls.push(method)
    return method === 'repair'
      ? {
          flag: false,
          data: 'Restart as administrator, then click Repair application again.',
        }
      : { flag: true, data: JSON.stringify(legacy) }
  })
  const store = useVPNGateStore()
  assert.equal(await store.run('repair', undefined), false)
  assert.match(store.error, /then click Repair application again/)
  assert.equal(store.state.repair.requiresAdmin, true)
  await store.refresh()
  assert.match(store.error, /Restart as administrator/)
  assert.deepEqual(calls, ['repair', 'state', 'state'], 'elevation never happens automatically')
})

test('cancelled administrator restart remains an error instead of showing repair success', async () => {
  setActivePinia(createPinia())
  bridge(async (method) => ({
    flag: true,
    data: JSON.stringify(method === 'runtime.elevate' ? { restarting: false } : state()),
  }))
  const store = useVPNGateStore()
  assert.equal(await store.run('runtime.elevate', undefined), false)
  assert.match(store.error, /restart was not started/)
  assert.equal(store.notice, '')
})

test('settings reject proxy loops, noninteger ports and embedded URL credentials', () => {
  const value = defaultVPNGateSettings()
  value.upstream.port = 1080
  assert.equal(validateVPNGateSettings(value), undefined)
  value.upstream.port = 1.5
  assert.match(validateVPNGateSettings(value), /port/)
  value.upstream.port = 20122
  assert.match(validateVPNGateSettings(value), /local mixed listener/)
  value.upstream.port = 1080
  value.testURL = 'https://private:secret@example.org/test'
  assert.match(validateVPNGateSettings(value), /without embedded credentials/)
})

test('settings accept the four mixed protocols and reject unsupported protocol tokens', () => {
  const value = defaultVPNGateSettings()
  value.upstream.port = 1080
  for (const protocol of ['http', 'socks4', 'socks4a', 'socks5']) {
    value.upstream.protocol = protocol
    assert.equal(validateVPNGateSettings(value), undefined, protocol)
    assert.equal(normalizeVPNGateState({ settings: value }).settings.upstream.protocol, protocol)
  }
  for (const protocol of ['https', 'socks5h', 'mixed', 'invalid']) {
    value.upstream.protocol = protocol
    assert.match(validateVPNGateSettings(value), /HTTP.*SOCKS4.*SOCKS4a.*SOCKS5/)
  }
})

test('SOCKS4 user IDs are accepted while unsupported passwords require explicit removal', () => {
  const value = defaultVPNGateSettings()
  value.upstream.port = 1080
  value.upstream.username = 'proxy-user-id'
  for (const protocol of ['socks4', 'socks4a']) {
    value.upstream.protocol = protocol
    assert.equal(validateVPNGateSettings(value), undefined)
    value.upstream.password = 'keep-until-user-clears'
    assert.match(validateVPNGateSettings(value), /SOCKS4.*password/)
    assert.equal(value.upstream.password, 'keep-until-user-clears')
    value.upstream.password = ''
    assert.equal(validateVPNGateSettings(value), undefined)
  }
})

test('invalid SOCKS4 credentials never reach settings RPC or erase the saved draft password', async () => {
  setActivePinia(createPinia())
  let calls = 0
  bridge(async () => {
    calls++
    return { flag: true, data: JSON.stringify(state()) }
  })
  const store = useVPNGateStore()
  const draft = defaultVPNGateSettings()
  draft.upstream.port = 1080
  draft.upstream.protocol = 'socks4a'
  draft.upstream.password = 'retained-draft-password'
  assert.equal(await store.saveSettings(draft), false)
  assert.equal(calls, 0)
  assert.match(store.error, /SOCKS4.*password/)
  assert.equal(draft.upstream.password, 'retained-draft-password')
})

test('node filtering combines status with case insensitive country/IP/hostname search', () => {
  const nodes = [
    {
      id: 'a',
      hostname: 'vpn-A',
      ip: '1.2.3.4',
      country: 'Japan',
      countryCode: 'JP',
      status: 'available',
    },
    {
      id: 'b',
      hostname: 'vpn-B',
      ip: '5.6.7.8',
      country: 'Japan',
      countryCode: 'JP',
      status: 'failed',
    },
  ]
  assert.deepEqual(
    filterVPNGateNodes(nodes, 'jAP', 'available').map((node) => node.id),
    ['a'],
  )
  assert.deepEqual(
    filterVPNGateNodes(nodes, '5.6', '').map((node) => node.id),
    ['b'],
  )
})

test('node filtering includes measured egress IP and location while preserving old node searches', () => {
  const nodes = [
    {
      id: 'measured',
      hostname: 'vpn-original',
      ip: '192.0.2.1',
      country: 'Japan',
      countryCode: 'JP',
      status: 'available',
      egressIP: '198.51.100.8',
      egressCountry: 'United States',
      egressCountryCode: 'US',
      egressRegion: 'California',
      egressCity: 'San Jose',
    },
    {
      id: 'legacy',
      hostname: 'vpn-old',
      ip: '192.0.2.2',
      country: 'Korea',
      countryCode: 'KR',
      status: 'failed',
    },
  ]
  for (const query of ['198.51.100.8', 'united STATES', 'US', '美国', 'calIFORNIA', 'san jose']) {
    assert.deepEqual(
      filterVPNGateNodes(nodes, query, 'available').map((node) => node.id),
      ['measured'],
    )
  }
  assert.deepEqual(
    filterVPNGateNodes(nodes, 'vpn-original', '').map((node) => node.id),
    ['measured'],
  )
  assert.deepEqual(
    filterVPNGateNodes(nodes, '192.0.2.2', '').map((node) => node.id),
    ['legacy'],
  )
  for (const query of ['Korea', 'South Korea', '韩国']) {
    assert.deepEqual(
      filterVPNGateNodes(nodes, query, '').map((node) => node.id),
      ['legacy'],
    )
  }
  assert.equal(filterVPNGateNodes(nodes, 'San Jose', 'failed').length, 0)
  assert.equal(filterVPNGateNodes(nodes, '', '').length, 2)
})

test('country names follow the UI language using measured codes and preserve unknown names', () => {
  assert.equal(formatVPNGateCountry('US', 'United States', 'zh-CN'), '美国')
  assert.equal(formatVPNGateCountry('JP', '日本', 'en'), 'Japan')
  assert.equal(formatVPNGateCountry(' jp ', 'Japan', 'zh-CN'), '日本')
  assert.equal(formatVPNGateCountry(undefined, '未识别地区', 'zh-CN'), '未识别地区')
  assert.equal(
    formatVPNGateCountry('invalid-code', 'Provider country', 'zh-CN'),
    'Provider country',
  )
  assert.equal(formatVPNGateCountry(undefined, undefined, 'zh-CN'), '')
})

test('measured region formatting handles missing values and duplicate region/city labels', () => {
  assert.equal(formatVPNGateRegion(undefined, undefined), '')
  assert.equal(formatVPNGateRegion('California', 'San Jose'), 'California / San Jose')
  assert.equal(formatVPNGateRegion(' Tokyo ', ' tokyo '), 'Tokyo')
  assert.equal(formatVPNGateRegion('', 'Seoul'), 'Seoul')
  assert.equal(formatVPNGateRegion('Osaka', ''), 'Osaka')
})

test('server endpoint formatting distinguishes IPv6 address from its port', () => {
  assert.equal(formatVPNGateEndpoint('192.0.2.1', 443), '192.0.2.1:443')
  assert.equal(formatVPNGateEndpoint('2001:db8::1', 443), '[2001:db8::1]:443')
})

test('RPC preserves exact typed payload and handles failed/malformed responses', async () => {
  const calls = []
  bridge(async (method, payload) => {
    calls.push([method, JSON.parse(payload)])
    return {
      flag: true,
      data: JSON.stringify({ id: 'job', status: 'running' }),
    }
  })
  assert.equal((await VPNGateRequest('tests.start', { ids: [] })).id, 'job')
  assert.deepEqual(calls, [['tests.start', { ids: [] }]])
  bridge(async () => ({
    flag: false,
    data: 'TUN requires administrator rights.',
  }))
  await assert.rejects(VPNGateRequest('connect', undefined), /TUN requires administrator rights/)
  bridge(async () => ({ flag: true, data: 'not JSON' }))
  await assert.rejects(VPNGateRequest('state', undefined), /invalid response/)
})

test('automatic polling never flashes the foreground refresh indicator across successful and failed ticks', async (context) => {
  context.mock.timers.enable({ apis: ['setTimeout'] })
  setActivePinia(createPinia())
  const pending = []
  let calls = 0
  bridge(() => {
    calls++
    return new Promise((resolve) => pending.push(resolve))
  })
  const store = useVPNGateStore()
  const loadingChanges = []
  const stopWatching = watch(
    () => store.loading,
    (value) => loadingChanges.push(value),
    { flush: 'sync' },
  )
  try {
    store.startPolling()
    for (let tick = 0; tick < 3; tick++) {
      if (tick) context.mock.timers.tick(2500)
      assert.equal(calls, tick + 1)
      assert.equal(store.loading, false, 'background reads must not animate manual refresh buttons')
      pending.shift()(
        tick === 1
          ? { flag: false, data: 'Temporary background read failure' }
          : { flag: true, data: JSON.stringify(state()) },
      )
      await flush()
      assert.equal(store.loading, false)
    }
    assert.deepEqual(
      loadingChanges,
      [],
      'a reactive button must see no loading transitions during polling',
    )
  } finally {
    store.stopPolling()
    for (const finish of pending) finish({ flag: true, data: JSON.stringify(state()) })
    await flush()
    stopWatching()
  }
})

test('manual refresh joins an in-flight background read with one foreground indicator and no duplicate RPC', async () => {
  setActivePinia(createPinia())
  let finish
  let calls = 0
  bridge(() => {
    calls++
    return new Promise((resolve) => {
      finish = resolve
    })
  })
  const store = useVPNGateStore()
  store.startPolling()
  const first = store.refresh()
  const second = store.refresh()
  let firstDone = false
  void first.then(() => {
    firstDone = true
  })
  try {
    await flush()
    assert.equal(store.loading, true)
    assert.equal(calls, 1)
    assert.equal(firstDone, false, 'a manual refresh must await the read it joined')
    finish({ flag: true, data: JSON.stringify(state()) })
    await Promise.all([first, second])
    assert.equal(store.loading, false)
    assert.equal(store.state.runtime.status, 'stopped')
  } finally {
    store.stopPolling()
    finish({ flag: true, data: JSON.stringify(state()) })
  }
})

test('post-operation state synchronization does not animate manual refresh buttons', async () => {
  setActivePinia(createPinia())
  let finishState
  const calls = []
  bridge((method) => {
    calls.push(method)
    return method === 'state'
      ? new Promise((resolve) => {
          finishState = resolve
        })
      : Promise.resolve({ flag: true, data: JSON.stringify(state()) })
  })
  const store = useVPNGateStore()
  const operation = store.run('connect', undefined)
  await flush()
  assert.equal(store.busy, 'connect')
  assert.equal(store.loading, false, 'only the connect operation should show its loading state')
  finishState({ flag: true, data: JSON.stringify(state()) })
  assert.equal(await operation, true)
  assert.deepEqual(calls, ['connect', 'state'])
  assert.equal(store.loading, false)
})

test('a stale state-read failure cannot flash an error after a newer successful mutation', async () => {
  setActivePinia(createPinia())
  let finishState
  const connected = state()
  connected.runtime.status = 'connected'
  bridge((method) =>
    method === 'state'
      ? new Promise((resolve) => {
          finishState = resolve
        })
      : Promise.resolve({ flag: true, data: JSON.stringify(connected) }),
  )
  const store = useVPNGateStore()
  const read = store.refresh()
  assert.equal(await store.run('connect', undefined), true)
  finishState({ flag: false, data: 'Obsolete state read failed' })
  await read
  assert.equal(store.state.runtime.status, 'connected')
  assert.equal(store.error, '', 'an obsolete request must not add an error banner to fresh state')
})

test('manual refresh clears its loading indicator after an explicit read error', async () => {
  setActivePinia(createPinia())
  let finish
  bridge(
    () =>
      new Promise((resolve) => {
        finish = resolve
      }),
  )
  const store = useVPNGateStore()
  const pending = store.refresh()
  assert.equal(store.loading, true)
  finish({ flag: false, data: 'Manual refresh failed' })
  await pending
  assert.equal(store.loading, false)
  assert.equal(store.error, 'Manual refresh failed')
})

test('polling serializes requests and cleanup ignores in-flight state without mutating runtime', async (context) => {
  context.mock.timers.enable({ apis: ['setTimeout'] })
  setActivePinia(createPinia())
  const calls = []
  let finish
  bridge((method) => {
    calls.push(method)
    return new Promise((resolve) => {
      finish = resolve
    })
  })
  const store = useVPNGateStore()
  store.startPolling()
  store.startPolling()
  context.mock.timers.tick(10000)
  assert.deepEqual(calls, ['state'])
  store.stopPolling()
  finish({ flag: true, data: JSON.stringify(state()) })
  await flush()
  context.mock.timers.tick(10000)
  assert.equal(store.state, undefined)
  assert.deepEqual(calls, ['state'])
})

test('mutations discard stale poll snapshots and never invoke legacy core RPCs', async () => {
  setActivePinia(createPinia())
  let finish
  const calls = []
  const connected = state()
  connected.runtime.status = 'connected'
  bridge((method) => {
    calls.push(method)
    return method === 'state'
      ? new Promise((resolve) => {
          finish = resolve
        })
      : Promise.resolve({ flag: true, data: JSON.stringify(connected) })
  })
  const store = useVPNGateStore()
  const polling = store.refresh()
  await store.run('connect', undefined)
  finish({ flag: true, data: JSON.stringify(state()) })
  await polling
  assert.equal(store.state.runtime.status, 'connected')
  assert.deepEqual(calls, ['state', 'connect'])
})

test('polls started during a mutation cannot overwrite its final state', async () => {
  setActivePinia(createPinia())
  let finishConnect
  let finishState
  bridge(
    (method) =>
      new Promise((resolve) => {
        if (method === 'connect') finishConnect = resolve
        else finishState = resolve
      }),
  )
  const store = useVPNGateStore()
  const mutation = store.run('connect', undefined)
  const polling = store.refresh()
  const connected = state()
  connected.runtime.status = 'connected'
  finishConnect({ flag: true, data: JSON.stringify(connected) })
  await mutation
  finishState({ flag: true, data: JSON.stringify(state()) })
  await polling
  assert.equal(store.state.runtime.status, 'connected')
})

test('background polling preserves actionable settings validation errors', async () => {
  setActivePinia(createPinia())
  bridge(async () => ({ flag: true, data: JSON.stringify(state()) }))
  const store = useVPNGateStore()
  const invalid = defaultVPNGateSettings()
  invalid.upstream.port = 0
  assert.equal(await store.saveSettings(invalid), false)
  await store.refresh()
  assert.match(store.error, /upstream proxy port/)
})

test('managed labels show only the selected existing locale', () => {
  assert.equal(getVPNGateText('节点池 · Node pool', 'zh'), '节点池')
  assert.equal(getVPNGateText('节点池 · Node pool', 'en'), 'Node pool')
  assert.equal(getVPNGateText('available', 'zh-CN'), '可用')
  assert.equal(getVPNGateText('available', 'en'), 'Available')
  assert.equal(getVPNGateText('VPNGate', 'en'), 'VPNGate')
})

test('edits made while settings RPC is pending stay unsaved and outside the submitted snapshot', async () => {
  const draft = defaultVPNGateSettings()
  draft.upstream.port = 1080
  draft.upstream.username = 'submitted'
  let revision = 1
  let finish
  let sent
  bridge((method, payload) => {
    assert.equal(method, 'settings.update')
    sent = JSON.parse(payload)
    return new Promise((resolve) => {
      finish = resolve
    })
  })
  const pending = submitVPNGateSettingsDraft(
    draft,
    'global',
    () => revision,
    async (snapshot) => {
      await VPNGateRequest('settings.update', snapshot)
      return true
    },
  )
  draft.upstream.username = 'edited during request'
  draft.upstream.processPaths.push('C:\\Proxy\\second.exe')
  revision++
  finish({ flag: true, data: JSON.stringify(state()) })
  assert.equal(await pending, false, 'the component must keep dirty and must not show Saved')
  assert.equal(draft.upstream.username, 'edited during request')
  assert.equal(sent.upstream.username, 'submitted')
  assert.deepEqual(sent.upstream.processPaths, [])
  assert.equal(sent.routingPreset, 'global')
})

test('an old Settings draft preserves the latest routing preset and submits its other edits', async () => {
  const draft = defaultVPNGateSettings()
  draft.upstream.port = 2080
  draft.refreshMinutes = 45
  assert.equal(draft.routingPreset, 'bypass-cn')
  let sent
  const committed = await submitVPNGateSettingsDraft(
    draft,
    'blacklist',
    () => 4,
    async (snapshot) => {
      sent = snapshot
      return true
    },
  )
  assert.equal(committed, true)
  assert.equal(sent.routingPreset, 'blacklist')
  assert.equal(sent.upstream.port, 2080)
  assert.equal(sent.refreshMinutes, 45)
  assert.equal(draft.routingPreset, 'bypass-cn', 'the helper does not mutate the editable draft')
})
