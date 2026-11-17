import type { VPNGateMethods } from '@/types/vpngate'

type VPNGateBridge = {
  VPNGateRequest: (method: string, payload: string) => Promise<{ flag: boolean; data: string }>
}

export async function VPNGateRequest<M extends keyof VPNGateMethods>(
  method: M,
  payload: VPNGateMethods[M]['payload'],
): Promise<VPNGateMethods[M]['result']> {
  const bridge = (window as unknown as { go?: { bridge?: { App?: VPNGateBridge } } }).go?.bridge
    ?.App
  if (!bridge?.VPNGateRequest)
    throw new Error('VPNGate desktop bridge is unavailable. Open the Windows desktop application.')
  const result = await bridge.VPNGateRequest(method, JSON.stringify(payload ?? {}))
  if (!result.flag) throw new Error(result.data || 'VPNGate request failed.')
  try {
    return JSON.parse(result.data)
  } catch {
    throw new Error('The VPNGate runtime returned an invalid response.')
  }
}
