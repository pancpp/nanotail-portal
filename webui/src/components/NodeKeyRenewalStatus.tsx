import { useI18n } from '../i18n'
import { Check, CircleAlert, KeyRound, LoaderCircle, X } from 'lucide-react'
import { renewalFeedback } from '../renewalFeedback'
import type { NodeKeyRenewalSnapshot } from '../useNodeKeyRenewal'

export default function NodeKeyRenewalStatus({ snapshot, compact = false, signIn = false }: { snapshot: NodeKeyRenewalSnapshot, compact?: boolean, signIn?: boolean }) {
  const { t } = useI18n()
  const feedback = renewalFeedback(snapshot, signIn)
  const Icon = feedback?.state === 'success' ? Check : feedback?.state === 'error' ? CircleAlert : feedback?.state === 'ready' ? KeyRound : feedback?.state === 'cancelled' ? X : LoaderCircle
  return <div className={`renewal-feedback${feedback ? ` renewal-feedback--${feedback.state}` : ''}${compact ? ' renewal-feedback--compact' : ''}`}
    role="status" aria-live="polite" aria-atomic="true">
    {feedback && <>
      <span className="renewal-feedback__icon" aria-hidden="true"><Icon className={feedback.state === 'waiting' ? 'spin' : ''} /></span>
      <div className="renewal-feedback__text"><strong>{t(feedback.title)}</strong><p>{t(feedback.description)}</p></div>
    </>}
  </div>
}
