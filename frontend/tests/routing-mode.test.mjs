import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'
import ts from 'typescript'
import * as Vue from 'vue'
import { compileScript, parse } from 'vue/compiler-sfc'

const { defaultVPNGateSettings, normalizeVPNGateState, routingOptions } =
  await import('../src/utils/vpngate.ts')

function loadComponent(vpn) {
  const filename = fileURLToPath(
    new URL('../src/components/_common/RoutingModeSwitch.vue', import.meta.url),
  )
  const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
  const script = compileScript(descriptor, { id: filename, inlineTemplate: true })
  const { outputText } = ts.transpileModule(script.content, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  })
  const dependencies = {
    vue: Vue,
    '@/stores/vpngate': { useVPNGateStore: () => vpn },
    '@/utils/vpngate': { routingOptions },
    '@/utils/vpngateText': { useVPNGateText: () => (value) => value },
  }
  const resolve = (specifier) => {
    assert.ok(specifier in dependencies, `Unexpected dependency: ${specifier}`)
    return dependencies[specifier]
  }
  const module = { exports: {} }
  new Function('require', 'module', 'exports', outputText)(resolve, module, module.exports)
  return module.exports.default
}

const element = (type, text = '') => ({ type, text, props: {}, children: [], parent: null })
const renderer = Vue.createRenderer({
  createElement: element,
  createText: (text) => element('#text', text),
  createComment: (text) => element('#comment', text),
  insert(node, parent, anchor = null) {
    if (node.parent) node.parent.children.splice(node.parent.children.indexOf(node), 1)
    const index = anchor ? parent.children.indexOf(anchor) : parent.children.length
    parent.children.splice(index, 0, node)
    node.parent = parent
  },
  remove(node) {
    node.parent.children.splice(node.parent.children.indexOf(node), 1)
    node.parent = null
  },
  setText: (node, text) => {
    node.text = text
  },
  setElementText(node, text) {
    node.text = text
    node.children = []
  },
  parentNode: (node) => node.parent,
  nextSibling: (node) => node.parent?.children[node.parent.children.indexOf(node) + 1] || null,
  patchProp: (node, name, _old, value) => {
    node.props[name] = value
  },
})

function descendants(node) {
  return [node, ...node.children.flatMap(descendants)]
}

function mountSwitch(context, preset = 'bypass-cn') {
  const calls = []
  const networkCalls = []
  let finishSave
  const vpn = Vue.reactive({
    state: normalizeVPNGateState({
      settings: { ...defaultVPNGateSettings(), routingPreset: preset },
      runtime: { status: 'connected', connectedNodeId: 'connected-node' },
    }),
    busy: '',
    // The real store marks itself busy synchronously, applies confirmed state
    // before its final refresh, and returns false when a save fails.
    async saveSettings(snapshot) {
      calls.push(JSON.parse(JSON.stringify(snapshot)))
      vpn.busy = 'settings.update'
      try {
        return await new Promise((resolve) => {
          finishSave = resolve
        })
      } finally {
        vpn.busy = ''
      }
    },
    async run(...args) {
      networkCalls.push(args)
      return true
    },
  })
  const container = element('container')
  const app = renderer.createApp(loadComponent(vpn))
  // Icons are decorative; exposing their names makes pending feedback testable
  // without coupling the routing interaction to SVG markup.
  // eslint-disable-next-line vue/multi-word-component-names
  app.component('Icon', {
    props: { icon: { type: String, default: '' } },
    setup: (props) => () => Vue.h('span', { 'data-icon': props.icon }),
  })
  app.mount(container)
  context.after(() => app.unmount())
  const root = container.children[0]
  const buttons = () => descendants(root).filter((node) => node.type === 'button')
  const button = (value) => buttons()[routingOptions.findIndex((option) => option.value === value)]
  return { vpn, calls, networkCalls, root, buttons, button, finish: (value) => finishSave(value) }
}

function assertSelected(switcher, preset) {
  for (const option of routingOptions) {
    const button = switcher.button(option.value)
    assert.equal(button.props['aria-pressed'], option.value === preset)
    assert.equal(button.props.class.split(/\s+/).includes('is-selected'), option.value === preset)
  }
}

test('renders all production routing modes as accessible native buttons', (context) => {
  const switcher = mountSwitch(context)
  assert.equal(switcher.root.props.role, 'group')
  assert.ok(switcher.root.props['aria-label'].includes('Routing mode'))
  assert.equal(switcher.buttons().length, routingOptions.length)
  for (const option of routingOptions) {
    const button = switcher.button(option.value)
    assert.equal(button.props.type, 'button')
    assert.equal(button.props.disabled, false)
    assert.equal(button.props['aria-busy'], undefined)
    assert.ok(descendants(button).some((node) => node.text === option.label))
  }
  assertSelected(switcher, 'bypass-cn')
})

for (const option of routingOptions) {
  test(`selecting ${option.value} saves the complete latest settings without a network action`, async (context) => {
    const initial = option.value === 'bypass-cn' ? 'direct' : 'bypass-cn'
    const switcher = mountSwitch(context, initial)
    const latest = {
      ...defaultVPNGateSettings(),
      upstream: {
        host: 'proxy.example.test',
        port: 18080,
        protocol: 'socks5',
        username: 'current-user',
        password: 'current-password',
        processPaths: ['C:\\Apps\\proxy.exe'],
      },
      routingPreset: initial,
      mixedPort: 22000,
      testConcurrency: 7,
      tunEnabled: false,
      corePath: 'C:\\Apps\\sing-box.exe',
    }
    switcher.vpn.state.settings = latest
    await Vue.nextTick()
    const originalRuntime = { ...switcher.vpn.state.runtime }
    const saving = switcher.button(option.value).props.onClick()
    const expected = { ...latest, routingPreset: option.value }
    assert.deepEqual(switcher.calls, [expected])
    assert.deepEqual(switcher.networkCalls, [], 'mode selection must only save settings')
    assert.deepEqual({ ...switcher.vpn.state.runtime }, originalRuntime)
    assertSelected(switcher, initial)

    // The backend response updates the store; only that confirmation changes
    // the selected mode, even while the store is completing its refresh.
    switcher.vpn.state.settings = expected
    await Vue.nextTick()
    assertSelected(switcher, option.value)
    switcher.finish(true)
    await saving
    await Vue.nextTick()
    assert.equal(switcher.vpn.busy, '')
    assertSelected(switcher, option.value)
    assert.deepEqual(switcher.networkCalls, [])
  })
}

test('pending save disables every mode and shows progress without selecting an unconfirmed mode', async (context) => {
  const switcher = mountSwitch(context)
  const saving = switcher.button('global').props.onClick()
  await Vue.nextTick()
  assert.equal(switcher.vpn.busy, 'settings.update')
  assertSelected(switcher, 'bypass-cn')
  for (const option of routingOptions) {
    const button = switcher.button(option.value)
    assert.equal(button.props.disabled, true)
    assert.equal(button.props['aria-busy'], option.value === 'global' ? true : undefined)
  }
  assert.ok(
    descendants(switcher.button('global')).some((node) => node.props['data-icon'] === 'loading'),
  )
  await switcher.button('direct').props.onClick()
  assert.equal(switcher.calls.length, 1, 'a second click while saving cannot start another save')
  switcher.vpn.state.settings.routingPreset = 'global'
  await Vue.nextTick()
  assertSelected(switcher, 'global')
  assert.equal(switcher.button('global').props['aria-busy'], true)
  switcher.finish(true)
  await saving
  await Vue.nextTick()
  for (const button of switcher.buttons()) {
    assert.equal(button.props.disabled, false)
    assert.equal(button.props['aria-busy'], undefined)
  }
  assert.ok(!descendants(switcher.root).some((node) => node.props['data-icon'] === 'loading'))
})

test('a failed save keeps the confirmed preset and clears pending feedback', async (context) => {
  const switcher = mountSwitch(context, 'blacklist')
  const saving = switcher.button('direct').props.onClick()
  await Vue.nextTick()
  switcher.finish(false)
  await saving
  await Vue.nextTick()
  assert.equal(switcher.calls.length, 1)
  assertSelected(switcher, 'blacklist')
  for (const button of switcher.buttons()) {
    assert.equal(button.props.disabled, false)
    assert.equal(button.props['aria-busy'], undefined)
  }
  assert.ok(!descendants(switcher.root).some((node) => node.props['data-icon'] === 'loading'))
  assert.deepEqual(switcher.networkCalls, [])
})

test('the selected mode, a busy store, and unavailable state never save settings', async (context) => {
  const switcher = mountSwitch(context)
  await switcher.button('bypass-cn').props.onClick()
  switcher.vpn.busy = 'connect'
  await Vue.nextTick()
  assert.ok(switcher.buttons().every((button) => button.props.disabled))
  await switcher.button('global').props.onClick()
  switcher.vpn.busy = ''
  switcher.vpn.state = undefined
  await Vue.nextTick()
  assert.ok(switcher.buttons().every((button) => button.props.disabled))
  await switcher.button('direct').props.onClick()
  assert.deepEqual(switcher.calls, [])
  assert.deepEqual(switcher.networkCalls, [])
})
