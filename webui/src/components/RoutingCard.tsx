import { useState } from 'react'
import { ChevronRight, Route } from 'lucide-react'
import { useTailscale } from '../tailscale'
import { routingSummary } from '../routing'
import ExitNodeDialog from './ExitNodeDialog'

export default function RoutingCard() {
  const { routing, routingError, refresh } = useTailscale()
  const [open, setOpen] = useState(false)
  const [saved, setSaved] = useState(false)
  const summary = routingSummary(routing, routingError)
  return <article className="status-card routing-card">
    <div className="status-card__topline">
      <span className="status-icon status-icon--violet"><Route size={20} /></span>
      <span className="quiet-label">ROUTING</span>
    </div>
    <div className="status-card__body">
      <span>Exit node</span><strong>{summary.title}</strong><p>{summary.detail}</p>
    </div>
    {saved && <p className="routing-saved" role="status">Routing settings saved.</p>}
    <button className="text-action" type="button" aria-haspopup="dialog" onClick={() => { setSaved(false); setOpen(true) }}>
      Configure <ChevronRight size={15} />
    </button>
    {open && <ExitNodeDialog onClose={() => setOpen(false)} onSaved={() => {
      setSaved(true); setOpen(false); void refresh()
    }} />}
  </article>
}
