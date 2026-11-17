type WebSocketsOptions = {
  base?: string
  bearer?: string
  beforeConnect?: () => void
}

type Options = { url: string; cb: (data: any) => void; params?: Record<string, any> }

export class WebSockets {
  public base: string
  public bearer: string
  public beforeConnect: () => void

  constructor(options: WebSocketsOptions) {
    this.base = options.base || ''
    this.bearer = options.bearer || ''
    this.beforeConnect = options.beforeConnect || (() => 0)
  }

  public createWS(options: Options) {
    this.beforeConnect()

    const params = { ...options.params, token: this.bearer }
    const query = new URLSearchParams(params).toString()
    const url = query ? `${options.url}?${query}` : options.url

    let isManualClose = false
    let ws: WebSocket | null = null
    let reconnectTimer: ReturnType<typeof setTimeout> | undefined
    let generation = 0

    const clearReconnect = () => {
      clearTimeout(reconnectTimer)
      reconnectTimer = undefined
    }

    const connect = () => {
      if (ws) return
      clearReconnect()
      isManualClose = false
      const current = ++generation
      const socket = new WebSocket(this.base + url)
      ws = socket
      socket.onmessage = (e) => {
        if (!isManualClose && ws === socket) options.cb(JSON.parse(e.data))
      }
      socket.onclose = () => {
        if (ws !== socket) return
        ws = null
        socket.onmessage = null
        socket.onclose = null
        reconnectTimer = setTimeout(() => {
          if (isManualClose || current !== generation) return
          reconnectTimer = undefined
          connect()
        }, 4000)
      }
    }

    const disconnect = () => {
      isManualClose = true
      generation++
      clearReconnect()
      if (ws) {
        ws.onmessage = null
        ws.onclose = null
        ws.close()
        ws = null
      }
    }

    return { connect, disconnect }
  }
}
