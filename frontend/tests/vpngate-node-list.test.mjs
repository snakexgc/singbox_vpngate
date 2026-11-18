import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'
import ts from 'typescript'
import * as Vue from 'vue'
import { compileScript, parse } from 'vue/compiler-sfc'
import { renderToString } from 'vue/server-renderer'

import * as utilities from '../src/utils/vpngate.ts'
import { getVPNGateText } from '../src/utils/vpngateLabels.ts'

/* eslint-disable vue/one-component-per-file -- The view test supplies global component stubs. */

const filename = fileURLToPath(new URL('../src/views/VPNGateView/index.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: filename, inlineTemplate: true })
const { outputText } = ts.transpileModule(script.content, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
})

const node = (overrides = {}) => ({
  id: 'node',
  hostname: 'vpn-original.example.test',
  ip: '192.0.2.4',
  port: 443,
  country: 'Japan',
  countryCode: 'JP',
  protocol: 'tcp',
  status: 'available',
  latencyMs: 125,
  ...overrides,
})

async function renderNodes(nodes, locale = 'en') {
  const vpn = {
    state: utilities.normalizeVPNGateState({ nodes }),
    activeJobs: [],
    busy: '',
    run: () => {},
  }
  const dependencies = {
    vue: Vue,
    'vue-i18n': { useI18n: () => ({ locale: Vue.ref(locale) }) },
    '@/stores/vpngate': { useVPNGateStore: () => vpn },
    '@/utils/vpngate': utilities,
    '@/utils/vpngateText': { useVPNGateText: () => (value) => getVPNGateText(value, locale) },
    '@/components/_common/StateBadge.vue': {
      __esModule: true,
      default: Vue.defineComponent({
        setup:
          (_, { slots }) =>
          () =>
            Vue.h('span', slots.default?.()),
      }),
    },
  }
  const module = { exports: {} }
  new Function('require', 'module', 'exports', outputText)(
    (specifier) => {
      assert.ok(specifier in dependencies, `Unexpected dependency: ${specifier}`)
      return dependencies[specifier]
    },
    module,
    module.exports,
  )
  const app = Vue.createSSRApp(module.exports.default)
  let columns
  // eslint-disable-next-line vue/multi-word-component-names
  app.component('Table', {
    props: {
      columns: { type: Array, required: true },
      dataSource: { type: Array, required: true },
    },
    setup:
      (props, { slots }) =>
      () => {
        columns = props.columns
        return Vue.h('table', [
          Vue.h('thead', [
            Vue.h(
              'tr',
              columns.map((column) => Vue.h('th', column.title)),
            ),
          ]),
          Vue.h(
            'tbody',
            props.dataSource.map((record) =>
              Vue.h(
                'tr',
                { 'data-node-id': record.id },
                columns.map((column) =>
                  Vue.h('td', { 'data-column': column.key }, slots[column.key]?.({ record })),
                ),
              ),
            ),
          ),
        ])
      },
  })
  for (const name of ['Button', 'Icon', 'RouterLink']) {
    app.component(name, {
      setup:
        (_, { slots }) =>
        () =>
          Vue.h('span', slots.default?.()),
    })
  }
  return { html: await renderToString(app), columns }
}

function cell(html, column) {
  const match = html.match(new RegExp(`<td data-column="${column}">([\\s\\S]*?)</td>`))
  assert.ok(match, `Missing ${column} cell`)
  return match[1]
}

test('node summary counts pending, available and failed nodes and exposes all five filters', async () => {
  const states = [
    ['untested', '待测试'],
    ['available', '可用'],
    ['failed', '失效'],
    ['testing', '测试中'],
    ['canceled', '已取消'],
  ]
  const nodes = [...states, states[0]].map(([status], id) => node({ id: String(id), status }))
  const { html } = await renderNodes(nodes, 'zh')
  const summary = html.match(/<div class="pool-summary">([\s\S]*?)<\/div>/)?.[1]
  assert.match(summary, />1 可用</)
  assert.match(summary, />1 失效</)
  assert.match(summary, />2 待测试</)
  assert.match(summary, />6 \/ 200</)
  for (const [value, label] of states) {
    assert.match(html, new RegExp(`<option value="${value}">${label}</option>`))
    const filtered = utilities.filterVPNGateNodes(nodes, '', value)
    assert.equal(filtered.length, value === 'untested' ? 2 : 1)
    assert.ok(filtered.every((item) => item.status === value))
  }
  assert.match(cell(html, 'status'), />待测试</)
  assert.match(html, />测试全部</)

  const { html: empty } = await renderNodes([], 'zh')
  for (const label of ['可用', '失效', '待测试']) {
    assert.match(empty, new RegExp(`>0 ${label}<`))
  }
})

test('node rows put official IP above the queried egress IP and use measured location', async () => {
  const measured = node({
    ip: '2001:db8::4',
    egressIP: '198.51.100.7',
    egressCountry: 'United States',
    egressCountryCode: 'US',
    egressRegion: 'California',
    egressCity: 'San Jose',
    egressCheckedAt: '2026-11-17T15:00:00Z',
    egressLanguage: 'zh-CN',
  })
  const { html, columns } = await renderNodes([measured])
  assert.match(html, /<th>IP<\/th>/)
  assert.match(cell(html, 'egressCountry'), />United States</)
  assert.doesNotMatch(cell(html, 'egressCountry'), /Japan/)
  assert.match(cell(html, 'egressRegion'), /California \/ San Jose/)
  const addresses = cell(html, 'ip')
  assert.match(addresses, /class="node-host">\[2001:db8::4\]:443/)
  assert.match(addresses, /class="node-endpoint">198\.51\.100\.7</)
  assert.ok(addresses.indexOf('[2001:db8::4]:443') < addresses.indexOf('198.51.100.7'))
  assert.match(addresses, /aria-label="Official VPNGate IP"/)
  assert.match(addresses, /aria-label="Egress IP"/)
  assert.match(addresses, /title="Egress checked: [^"]+Cached for 7 days"/)
  assert.match(addresses, /title="Host: vpn-original\.example\.test"/)
  assert.match(cell(html, 'status'), />Available</)
  assert.match(cell(html, 'latencyMs'), /125 ms/)
  const other = node({
    country: 'Zambia',
    egressCountry: 'Germany',
    egressCountryCode: 'DE',
    egressIP: '198.51.100.20',
    egressRegion: 'Berlin',
  })
  assert.ok(columns.find((column) => column.key === 'egressCountry').sort(measured, other) > 0)
  assert.ok(columns.find((column) => column.key === 'ip').sort(measured, other) > 0)
  assert.ok(columns.find((column) => column.key === 'egressRegion').sort(measured, other) > 0)
})

test('untested and legacy nodes show the official country until an egress location is measured', async () => {
  const pending = node({ status: 'untested', latencyMs: null })
  const measured = node({ egressIP: '198.51.100.7', egressCountryCode: 'AU' })
  const { html, columns } = await renderNodes([pending])
  assert.match(cell(html, 'egressCountry'), />Japan</)
  assert.match(cell(html, 'egressRegion'), /—/)
  assert.match(cell(html, 'ip'), /class="node-host">192\.0\.2\.4:443/)
  assert.match(cell(html, 'ip'), /class="node-endpoint">—</)
  assert.match(cell(html, 'ip'), /Egress not measured/)
  assert.equal((html.match(/Egress not measured/g) || []).length, 1)
  assert.ok(columns.find((column) => column.key === 'egressCountry').sort(pending, measured) > 0)
  assert.match(html, /Shows the VPN Gate country until the egress location is measured/)

  const { html: legacyHtml } = await renderNodes([node()], 'zh-CN')
  assert.match(cell(legacyHtml, 'egressCountry'), />日本</)
})

test('egress provider failure is disclosed without changing availability or latency', async () => {
  const { html } = await renderNodes([node({ egressError: 'Provider limit <retry later>' })], 'zh')
  assert.match(cell(html, 'egressCountry'), />日本</)
  assert.match(cell(html, 'egressRegion'), /—/)
  assert.match(cell(html, 'ip'), /<details/)
  assert.match(cell(html, 'ip'), /<summary>查询失败<\/summary>/)
  assert.match(cell(html, 'ip'), /Provider limit &lt;retry later&gt;/)
  assert.match(cell(html, 'ip'), /class="node-endpoint">—</)
  assert.match(cell(html, 'status'), />可用</)
  assert.match(cell(html, 'latencyMs'), /125 ms/)
})

test('exit backfill targets available nodes without a fresh seven-day measurement', async () => {
  const measured = { egressIP: '198.51.100.7', egressCountryCode: 'US' }
  const { html } = await renderNodes(
    [
      node({ id: 'legacy' }),
      node({ id: 'untested', status: 'untested' }),
      node({ id: 'fresh', ...measured, egressCheckedAt: new Date().toISOString() }),
      node({
        id: 'expired',
        ...measured,
        egressCheckedAt: new Date(Date.now() - 8 * 86400000).toISOString(),
      }),
    ],
    'zh-CN',
  )
  assert.match(html, />补测出口 \(2\)</)
  assert.match(html, /待补测出口/)
  assert.equal((html.match(/出口缓存已过期/g) || []).length, 1)
})

test('Chinese node rows localize legacy country names and show localized region data', async () => {
  const { html } = await renderNodes(
    [
      node({
        egressIP: '198.51.100.9',
        egressCountry: 'Japan',
        egressCountryCode: 'JP',
        egressRegion: '东京都',
        egressCity: '千代田区',
        egressLanguage: 'zh-CN',
      }),
    ],
    'zh-CN',
  )
  assert.match(cell(html, 'egressCountry'), />日本</)
  assert.match(cell(html, 'egressRegion'), /东京都 \/ 千代田区/)
  assert.match(cell(html, 'ip'), />官方</)
  assert.match(cell(html, 'ip'), />出口</)
  assert.match(html, /出口信息缓存 7 天/)
})
