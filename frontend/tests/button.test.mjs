import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { test } from 'node:test'
import { fileURLToPath, pathToFileURL } from 'node:url'
import ts from 'typescript'
import * as Vue from 'vue'
import { compileScript, compileStyleAsync, parse } from 'vue/compiler-sfc'

const require = createRequire(import.meta.url)

function loadComponent(path) {
  const filename = fileURLToPath(new URL(path, import.meta.url))
  const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
  const script = compileScript(descriptor, { id: filename, inlineTemplate: true })
  const { outputText } = ts.transpileModule(script.content, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  })
  const module = { exports: {} }
  const resolve = (specifier) =>
    specifier === 'vue'
      ? Vue
      : require(fileURLToPath(new URL(`${specifier}.ts`, pathToFileURL(filename))))
  new Function('require', 'module', 'exports', outputText)(resolve, module, module.exports)
  return { component: module.exports.default, descriptor, filename }
}

const {
  component: Button,
  descriptor,
  filename,
} = loadComponent('../src/components/Button/index.vue')
const { component: Icon } = loadComponent('../src/components/Icon/index.vue')
const { icons } = await import('../src/components/Icon/icons.ts')

// Use the compiled production declarations to distinguish normal flow from an
// overlay. This renderer intentionally checks layout participation, not pixels.
const classStyles = new Map()
const styles = [
  ...descriptor.styles.map((style) => ({
    source: style.content,
    preprocessLang: style.lang,
    scoped: style.scoped,
  })),
  ...['display', 'others'].map((name) => ({
    source: readFileSync(
      new URL(`../src/assets/styles/utilities/${name}.less`, import.meta.url),
      'utf8',
    ),
    preprocessLang: 'less',
  })),
]
for (const style of styles) {
  const result = await compileStyleAsync({ ...style, filename, id: 'data-v-button-test' })
  assert.deepEqual(result.errors, [])
  result.rawResult.root.walkRules((rule) => {
    const match = /^\.([\w-]+)(?:\[data-v-button-test\])?$/.exec(rule.selector)
    if (!match) return
    const declarations = classStyles.get(match[1]) || {}
    rule.nodes
      .filter((node) => node.type === 'decl')
      .forEach((node) => {
        declarations[node.prop] = node.value
      })
    classStyles.set(match[1], declarations)
  })
}

function computedStyle(node) {
  const style = {}
  for (const className of (node.props.class || '').split(/\s+/)) {
    Object.assign(style, classStyles.get(className))
  }
  return Object.assign(style, node.props.style)
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

function mountButton(
  context,
  props,
  label = () => [Vue.h('span', { 'data-label': '' }, 'Refresh')],
) {
  const state = Vue.reactive({ loading: false, disabled: false, ...props })
  const container = element('container')
  const app = renderer.createApp({
    render: () => Vue.h(Button, state, label ? { default: label } : {}),
  })
  // Use the production registration expected by the compiled Button template.
  // eslint-disable-next-line vue/multi-word-component-names
  app.component('Icon', Icon)
  app.mount(container)
  context.after(() => app.unmount())
  return { state, root: container.children[0] }
}

function descendants(node) {
  return [node, ...node.children.flatMap(descendants)]
}

function flowShape(node) {
  if (node.type === '#comment' || (node.type === '#text' && !node.text)) return []
  const style = computedStyle(node)
  if (style.position === 'absolute' || style.position === 'fixed' || style.display === 'none')
    return []
  return [
    {
      type: node.type,
      text: node.text,
      html: node.props.innerHTML,
      width: style.width,
      height: style.height,
      children: node.children.flatMap(flowShape),
    },
  ]
}

test('renders a native non-submit button while preserving custom roles and accessible labels', (context) => {
  const { root } = mountButton(context, {
    role: 'tab',
    'aria-label': 'Refresh VPNGate nodes',
  })
  assert.equal(root.type, 'button', 'native buttons support keyboard focus and activation')
  assert.equal(root.props.type, 'button', 'actions must not submit a surrounding form')
  assert.equal(root.props.role, 'tab')
  assert.equal(root.props['aria-label'], 'Refresh VPNGate nodes')
  assert.equal(root.props.disabled, false)
  assert.equal(root.props['aria-busy'], undefined)
})

test('decorative button icons stay out of the accessible name', (context) => {
  const { root } = mountButton(context, { icon: 'refresh', loading: true })
  const icon = descendants(root).find((node) => node.props.innerHTML === icons.refresh)
  assert.ok(icon)
  let decorative = icon
  while (decorative && decorative.props['aria-hidden'] !== 'true') decorative = decorative.parent
  assert.ok(decorative, 'a decorative icon must have an aria-hidden ancestor')
})

for (const [size, dimension] of [
  ['default', '16px'],
  ['small', '14px'],
  ['large', '16px'],
]) {
  test(`${size} spinner-only button retains the original icon-sized footprint`, async (context) => {
    const { state, root } = mountButton(context, { size, loading: true }, null)
    const loadingShape = flowShape(root)
    const flowContent = root.children.find(
      (node) => node.type !== '#comment' && computedStyle(node).position !== 'absolute',
    )
    assert.equal(computedStyle(flowContent).width, dimension)
    assert.equal(computedStyle(flowContent).height, dimension)
    state.loading = false
    await Vue.nextTick()
    assert.deepEqual(flowShape(root), loadingShape)
  })
}

for (const fixture of [
  { name: 'text-only', props: {}, label: () => ['Refresh'] },
  { name: 'icon and slotted content', props: { icon: 'refresh', iconSize: 32 } },
  { name: 'icon-only', props: { icon: 'refresh', iconSize: 32 }, label: null },
]) {
  test(`${fixture.name} keeps its normal-flow content during disabled and loading transitions`, async (context) => {
    const { state, root } = mountButton(context, fixture.props, fixture.label)
    const idleShape = flowShape(root)
    const originalContent = descendants(root).filter(
      (node) => node.text === 'Refresh' || node.props.innerHTML,
    )
    for (const next of [
      { disabled: true, loading: false },
      { disabled: false, loading: true },
      { disabled: true, loading: true },
      { disabled: false, loading: false },
    ]) {
      Object.assign(state, next)
      await Vue.nextTick()
      assert.deepEqual(
        flowShape(root),
        idleShape,
        `normal flow changed for ${JSON.stringify(next)}`,
      )
      for (const content of originalContent) {
        assert.ok(
          descendants(root).includes(content),
          'original icon/slot content must remain mounted',
        )
      }
    }
  })
}

test('loading exposes a visible spinner overlay while keeping the original content footprint', async (context) => {
  const { state, root } = mountButton(context, { size: 'small', icon: 'refresh', iconSize: 32 })
  state.loading = true
  await Vue.nextTick()
  const spinner = descendants(root).find((node) => node.props.innerHTML === icons.loading)
  assert.ok(spinner, 'manual loading still renders the real loading icon')
  let overlay = spinner
  while (overlay && computedStyle(overlay).position !== 'absolute') overlay = overlay.parent
  assert.ok(overlay, 'loading feedback must not add a normal-flow flex item')
  assert.equal(computedStyle(root).position, 'relative', 'overlay stays anchored to its own button')
  assert.equal(computedStyle(overlay).inset, '0', 'overlay covers and centers within the button')
  let visible = spinner
  while (visible && visible !== root) {
    assert.notEqual(computedStyle(visible).visibility, 'hidden', 'spinner must be visible')
    assert.notEqual(computedStyle(visible).opacity, '0', 'spinner must be visible')
    visible = visible.parent
  }
  const label = descendants(root).find((node) => node.text === 'Refresh')
  let hiddenContent = label
  while (hiddenContent && computedStyle(hiddenContent).opacity !== '0')
    hiddenContent = hiddenContent.parent
  assert.ok(
    hiddenContent,
    'content is visually hidden while retaining its footprint and accessible label',
  )
  assert.notEqual(computedStyle(hiddenContent).visibility, 'hidden')
})

test('disabled and loading block native activation and expose their states without a forbidden glyph', async (context) => {
  const { state, root } = mountButton(context, {})
  assert.equal(root.props.role, 'button')
  for (const next of [
    { disabled: true, loading: false },
    { disabled: false, loading: true },
    { disabled: true, loading: true },
  ]) {
    Object.assign(state, next)
    await Vue.nextTick()
    assert.equal(root.props.disabled, true, 'native disabled blocks mouse and keyboard activation')
    assert.equal(root.props['aria-disabled'], true)
    assert.equal(root.props['aria-busy'], next.loading ? true : undefined)
    assert.equal(computedStyle(root)['pointer-events'], 'none')
    if (next.disabled) assert.equal(computedStyle(root).opacity, '0.5')
    assert.ok(!descendants(root).some((node) => node.props.innerHTML === icons.forbidden))
  }
  Object.assign(state, { disabled: false, loading: false })
  await Vue.nextTick()
  assert.equal(root.props.disabled, false)
  assert.equal(root.props['aria-disabled'], undefined)
  assert.equal(root.props['aria-busy'], undefined)
  assert.notEqual(computedStyle(root)['pointer-events'], 'none')
})
