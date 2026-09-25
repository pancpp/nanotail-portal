import type { NodeKeyRenewalSnapshot } from './useNodeKeyRenewal'

export interface RenewalFeedback {
  state: 'ready' | 'cancelled' | 'waiting' | 'success' | 'error'
  title: string
  description: string
}

export function renewalFeedback(snapshot: NodeKeyRenewalSnapshot, signIn = false): RenewalFeedback | null {
  if (snapshot.error) return {
    state: 'error', title: signIn ? 'Sign-in not confirmed' : 'Renewal not confirmed',
    description: signIn ? 'Reconnect over the LAN and check sign-in status.' : 'Reconnect over the LAN and check renewal status.',
  }
  if (snapshot.cancelling) return { state: 'waiting', title: signIn ? 'Cancelling sign-in…' : 'Cancelling renewal…', description: 'Waiting for backend confirmation. Tailscale has not been changed.' }
  if (snapshot.preparing) return { state: 'waiting', title: signIn ? 'Preparing sign-in…' : 'Preparing renewal…', description: 'Authentication will start only after you choose Sign in.' }
  if (snapshot.value?.state === 'READY') return { state: 'ready', title: 'Ready to sign in', description: signIn ? 'Choose Sign in to connect, or close the panel to cancel.' : 'Choose Sign in to renew, or close the panel to cancel.' }
  if (snapshot.value?.state === 'CANCELLED') return { state: 'cancelled', title: signIn ? 'Sign-in cancelled' : 'Renewal cancelled', description: 'The request was cancelled without changing Tailscale.' }
  if (snapshot.starting || snapshot.pending) {
    let description = signIn ? 'Checking sign-in status…' : 'Checking renewal status…'
    if (snapshot.starting) description = signIn ? 'Starting sign-in…' : 'Starting renewal…'
    else switch (snapshot.value?.state) {
      case 'STARTING': description = signIn ? 'Preparing your sign-in link. This device is not connected yet.' : 'Preparing your sign-in link. Renewal is not complete yet.'; break
      case 'AWAITING_LOGIN': description = 'Complete Tailscale sign-in. This page will update automatically.'; break
      case 'AWAITING_APPROVAL': description = 'Waiting for device approval from your tailnet administrator.'; break
    }
    return { state: 'waiting', title: signIn ? 'Waiting for sign-in…' : 'Waiting for renewal…', description }
  }
  if (snapshot.value?.state === 'SIGNED_IN' || (signIn && snapshot.value?.state === 'COMPLETE')) return {
    state: 'success', title: 'Signed in to Tailscale successfully',
    description: 'This device is connected to your tailnet.',
  }
  if (snapshot.value?.state === 'COMPLETE') return {
    state: 'success', title: 'Node key renewed successfully',
    description: 'Tailscale confirmed a new node key.',
  }
  return null
}
