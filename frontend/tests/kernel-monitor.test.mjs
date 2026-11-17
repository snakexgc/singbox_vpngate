import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import * as Pinia from 'pinia'
import ts from 'typescript'
import * as Vue from 'vue'

function loadMonitor(vpn) {
  const telemetryCalls = []
  const telemetry = Object.fromEntries(
    ['onLogs', 'onMemory', 'onTraffic', 'onConnections'].map((name) => [
      name,
      (callback) => {
        const unsubscribe = () => {}
        telemetryCalls.push({ name, callback, unsubscribe })
        return unsubscribe
      },
    ]),
  )
  const unexpected = new Proxy(
    {},
    {
      get: (_, name) => () => {
        throw new Error(`The monitor invoked a legacy dependency: ${String(name)}`)
      },
    },
  )
  const { outputText } = ts.transpileModule(
    readFileSync(new URL('../src/stores/kernelApi.ts', import.meta.url), 'utf8'),
    { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } },
  )
  const dependencies = {
    pinia: Pinia,
    vue: Vue,
    '@/api/kernel': telemetry,
    '@/stores/vpngate': { useVPNGateStore: () => vpn },
  }
  const module = { exports: {} }
  new Function('require', 'module', 'exports', outputText)(
    (name) => dependencies[name] || unexpected,
    module,
    module.exports,
  )
  Pinia.setActivePinia(Pinia.createPinia())
  return { monitor: module.exports.useKernelApiStore(), telemetryCalls }
}

function runtime(status = 'stopped') {
  return Vue.reactive({ state: { runtime: { status }, settings: { mixedPort: 20122 } }, busy: '' })
}

test('monitor follows managed state without invoking legacy runtime dependencies', () => {
  const vpn = runtime()
  const { monitor } = loadMonitor(vpn)
  assert.equal(monitor.running, false)
  assert.equal(monitor.starting, false)
  vpn.state.runtime.status = 'connected'
  assert.equal(monitor.running, true)
  vpn.state.runtime.status = 'starting'
  assert.equal(monitor.starting, true)
  vpn.busy = 'stop'
  assert.equal(monitor.stopping, true)
  for (const method of ['startCore', 'stopCore', 'restartCore', 'updateConfig']) {
    assert.equal(method in monitor, false, `${method} must not expose a second runtime owner`)
  }
})

test('request proxy endpoint uses only the ready managed mixed listener', () => {
  const vpn = runtime('connected')
  const { monitor } = loadMonitor(vpn)
  const expected = {
    schema: 'http',
    host: '127.0.0.1',
    port: 20122,
    username: '',
    password: '',
    proxyType: 'mixed',
  }
  assert.deepEqual(monitor.getProxyEndpoint(), expected)
  vpn.state.runtime.status = 'direct'
  vpn.state.settings.mixedPort = 23456
  assert.deepEqual(monitor.getProxyEndpoint(), { ...expected, port: 23456 })
  for (const status of ['stopped', 'starting', 'protected', 'error']) {
    vpn.state.runtime.status = status
    assert.equal(monitor.getProxyEndpoint(), undefined, status)
  }
  vpn.state.runtime.status = 'connected'
  for (const port of [0, -1, 65536, 1.5, NaN]) {
    vpn.state.settings.mixedPort = port
    assert.equal(monitor.getProxyEndpoint(), undefined, `invalid port: ${port}`)
  }
  vpn.state = undefined
  assert.equal(monitor.getProxyEndpoint(), undefined)
})

test('monitor preserves all four telemetry subscriptions', () => {
  const { monitor, telemetryCalls } = loadMonitor(runtime())
  for (const name of ['onLogs', 'onMemory', 'onTraffic', 'onConnections']) {
    const callback = () => {}
    const unsubscribe = monitor[name](callback)
    const call = telemetryCalls.at(-1)
    assert.equal(call.name, name)
    assert.equal(call.callback, callback)
    assert.equal(unsubscribe, call.unsubscribe)
  }
})
