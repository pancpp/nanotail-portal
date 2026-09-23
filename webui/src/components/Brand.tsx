import { Network } from 'lucide-react'

interface BrandProps {
  compact?: boolean
}

export default function Brand({ compact = false }: BrandProps) {
  return (
    <div className={`brand${compact ? ' brand--compact' : ''}`}>
      <span className="brand__mark" aria-hidden="true">
        <Network size={compact ? 19 : 22} strokeWidth={2.2} />
      </span>
      <span className="brand__name">
        nanotail <span>portal</span>
      </span>
    </div>
  )
}
