import assert from 'node:assert/strict'
import { test } from 'node:test'

import { WebSockets } from '../src/api/websocket.ts'

const setup = (context) => {
  context.mock.timers.enable({ apis: ['setTimeout'] })
  const sockets = []
  const originalWebSocket = globalThis.WebSocket
  globalThis.WebSocket = class {
    onmessage = null
    onclose = null
    closed = false

    constructor(url) {
      this.url = url
      sockets.push(this)
    }

    close() {
      this.closed = true
    }
  }
  context.after(() => {
    globalThis.WebSocket = originalWebSocket
  })
  const received = []
  const channel = new WebSockets({ base: 'ws://127.0.0.1:20123' }).createWS({
    url: '/traffic',
    cb: (data) => received.push(data),
  })
  const advanceRetry = () => {
    context.mock.timers.tick(1000)
    context.mock.timers.tick(2999)
    context.mock.timers.tick(1)
  }
  return { channel, sockets, received, advanceRetry }
}

test('remote close reconnects once after the existing retry delay', (context) => {
  const { channel, sockets } = setup(context)
  channel.connect()
  sockets[0].onclose()
  context.mock.timers.tick(1000)
  context.mock.timers.tick(2999)
  assert.equal(sockets.length, 1)
  context.mock.timers.tick(1)
  assert.equal(sockets.length, 2)
  channel.disconnect()
})

for (const delay of [0, 1000, 3999]) {
  test(`disconnect cancels a retry after ${delay} ms`, (context) => {
    const { channel, sockets, advanceRetry } = setup(context)
    channel.connect()
    sockets[0].onclose()
    if (delay >= 1000) context.mock.timers.tick(1000)
    if (delay > 1000) context.mock.timers.tick(delay - 1000)
    channel.disconnect()
    advanceRetry()
    assert.equal(sockets.length, 1, 'a disposed channel must not reopen')
  })
}

test('disconnect ignores an already queued message callback', (context) => {
  const { channel, sockets, received } = setup(context)
  channel.connect()
  const queuedMessage = sockets[0].onmessage
  channel.disconnect()
  queuedMessage({ data: '{"up":7,"down":9}' })
  assert.deepEqual(received, [])
})

test('explicit connect after disconnect accepts messages and retries again', (context) => {
  const { channel, sockets, received, advanceRetry } = setup(context)
  channel.connect()
  channel.disconnect()
  channel.connect()
  sockets[1].onmessage({ data: '{"up":7,"down":9}' })
  assert.deepEqual(received, [{ up: 7, down: 9 }])
  sockets[1].onclose()
  advanceRetry()
  assert.equal(sockets.length, 3)
  channel.disconnect()
})

test('repeated connect does not duplicate an active socket', (context) => {
  const { channel, sockets } = setup(context)
  channel.connect()
  channel.connect()
  assert.equal(sockets.length, 1)
  channel.disconnect()
})

test('explicit connect replaces a pending retry without creating another socket later', (context) => {
  const { channel, sockets, advanceRetry } = setup(context)
  channel.connect()
  sockets[0].onclose()
  channel.connect()
  advanceRetry()
  assert.equal(sockets.length, 2)
  channel.disconnect()
})

test('old socket callbacks cannot update or reconnect a restarted channel', (context) => {
  const { channel, sockets, received, advanceRetry } = setup(context)
  channel.connect()
  const oldClose = sockets[0].onclose
  const oldMessage = sockets[0].onmessage
  channel.disconnect()
  channel.connect()
  oldClose()
  oldMessage({ data: '{"up":77,"down":99}' })
  advanceRetry()
  assert.equal(sockets.length, 2)
  assert.deepEqual(received, [])
  sockets[1].onmessage({ data: '{"up":1,"down":2}' })
  assert.deepEqual(received, [{ up: 1, down: 2 }])
  channel.disconnect()
})

test('repeated close callbacks schedule only one replacement', (context) => {
  const { channel, sockets, advanceRetry } = setup(context)
  channel.connect()
  const oldClose = sockets[0].onclose
  oldClose()
  oldClose()
  advanceRetry()
  assert.equal(sockets.length, 2)
  oldClose()
  advanceRetry()
  assert.equal(sockets.length, 2)
  channel.disconnect()
})
