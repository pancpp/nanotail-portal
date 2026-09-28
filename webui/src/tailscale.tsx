import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from 'react'
import { useAuth } from './auth'
import { useNodeKeyRenewal } from './useNodeKeyRenewal'
import { useCredentialSetup } from './useCredentialSetup'
import {
  clearTailscaleCredentialRequest, isSessionError, setTailscaleCredentialRequest,
  tailscaleClientRequest, tailscaleStatusRequest,
  type TailscaleClient, type TailscaleCredential, type TailscaleStatus,
  tailscaleRoutingRequest, type TailscaleRouting,
} from './api'

interface TailscaleContextValue {
  status: TailscaleStatus | null
  client: TailscaleClient | null | undefined
  statusError: string
  clientError: string
  routing: TailscaleRouting | null
  routingError: string
  refreshing: boolean
  refresh: () => Promise<void>
  save: (credential: TailscaleCredential) => Promise<void>
  clear: () => Promise<void>
  keyRenewalActive: boolean
  keyRenewalDialogOpen: boolean
  credentialSetup: ReturnType<typeof useCredentialSetup>
  keyRenewal: ReturnType<typeof useNodeKeyRenewal>
  setKeyRenewalDialogOpen: (open: boolean) => void
}

const TailscaleContext = createContext<TailscaleContextValue | null>(null)

export function TailscaleProvider({ children }: { children: ReactNode }) {
  const { accessToken, logout } = useAuth()
  const [status, setStatus] = useState<TailscaleStatus | null>(null)
  const [client, setClient] = useState<TailscaleClient | null>()
  const [statusError, setStatusError] = useState('')
  const [clientError, setClientError] = useState('')
  const [routing, setRouting] = useState<TailscaleRouting | null>(null)
  const [routingError, setRoutingError] = useState('')
  const [refreshing, setRefreshing] = useState(false)
  const [keyRenewalDialogOpen, setKeyRenewalDialogOpen] = useState(false)
  const pending = useRef<AbortController | null>(null)

  const refresh = useCallback(async () => {
    if (!accessToken) return
    pending.current?.abort()
    const controller = new AbortController()
    pending.current = controller
    setRefreshing(true)
    await Promise.all([
      tailscaleRoutingRequest(accessToken, controller.signal).then((value) => {
        if (!controller.signal.aborted) { setRouting(value); setRoutingError('') }
      }).catch((error: unknown) => {
        if (controller.signal.aborted) return
        if (isSessionError(error)) logout()
        setRoutingError(error instanceof Error ? error.message : 'Unable to read routing settings.')
      }),
      tailscaleStatusRequest(accessToken, controller.signal).then((value) => {
        if (!controller.signal.aborted) { setStatus(value); setStatusError('') }
      }).catch((error: unknown) => {
        if (controller.signal.aborted) return
        if (isSessionError(error)) logout()
        setStatusError(error instanceof Error ? error.message : 'Unable to read Tailscale status.')
      }),
      tailscaleClientRequest(accessToken, controller.signal).then((value) => {
        if (!controller.signal.aborted) { setClient(value); setClientError('') }
      }).catch((error: unknown) => {
        if (controller.signal.aborted) return
        if (isSessionError(error)) logout()
        setClientError(error instanceof Error ? error.message : 'Unable to load credential settings.')
      }),
    ])
    if (!controller.signal.aborted) setRefreshing(false)
  }, [accessToken, logout])

  const keyRenewal = useNodeKeyRenewal(accessToken, logout, refresh)
  const credentialSetup = useCredentialSetup(status, statusError)
  const keyRenewalActive = keyRenewalDialogOpen || keyRenewal.pending

  useEffect(() => {
    void refresh()
    return () => { pending.current?.abort() }
  }, [refresh])

  async function save(credential: TailscaleCredential) {
    if (!accessToken) throw new Error('Please sign in again.')
    try {
      await setTailscaleCredentialRequest(accessToken, credential)
      pending.current?.abort()
      setClient({ clientId: credential.clientId.trim(), hasClientSecret: true, updateTime: new Date().toISOString() })
      setClientError('')
      void refresh()
    } catch (error) { if (isSessionError(error)) logout(); throw error }
  }

  async function clear() {
    if (!accessToken) throw new Error('Please sign in again.')
    try {
      await clearTailscaleCredentialRequest(accessToken)
      pending.current?.abort()
      setClient(null)
      setClientError('')
      void refresh()
    } catch (error) { if (isSessionError(error)) logout(); throw error }
  }

  return <TailscaleContext.Provider value={{ status, client, statusError, clientError, routing, routingError, refreshing, refresh, save, clear, keyRenewalActive, keyRenewalDialogOpen, keyRenewal, setKeyRenewalDialogOpen, credentialSetup }}>
    {children}
  </TailscaleContext.Provider>
}

export function useTailscale() {
  const context = useContext(TailscaleContext)
  if (!context) throw new Error('useTailscale must be used inside TailscaleProvider')
  return context
}
