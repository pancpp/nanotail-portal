import { useEffect, useId, useLayoutEffect, useRef, useState, type ComponentPropsWithoutRef } from 'react'

type ButtonProps = Omit<ComponentPropsWithoutRef<'button'>, 'disabled'> & {
  /** A translated, actionable explanation. An empty reason enables the button. */
  disabledReason?: string
}

// The native button stays disabled. Its focusable wrapper lets mouse, keyboard,
// and touch users discover the reason without submitting a form or running an action.
export default function Button({ disabledReason = '', children, ...props }: ButtonProps) {
  const id = useId()
  const buttonId = props.id ?? `${id}-button`
  const tooltipId = `${id}-reason`
  const disabled = !!disabledReason
  const anchor = useRef<HTMLSpanElement>(null)
  const button = useRef<HTMLButtonElement>(null)
  const tooltip = useRef<HTMLSpanElement>(null)
  const closeTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const [open, setOpen] = useState(false)

  function show() {
    clearTimeout(closeTimer.current)
    if (disabled) setOpen(true)
  }
  function hide() {
    clearTimeout(closeTimer.current)
    setOpen(false)
  }

  useEffect(() => () => clearTimeout(closeTimer.current), [])
  useEffect(() => {
    if (!disabled) {
      setOpen(false)
      if (document.activeElement === anchor.current) button.current?.focus()
    }
  }, [disabled])

  useLayoutEffect(() => {
    const bubble = tooltip.current
    const trigger = anchor.current
    if (!open || !disabled || !bubble || !trigger) return
    // The top layer keeps tooltips visible inside scrolling modal dialogs.
    bubble.showPopover()
    function position() {
      const rect = trigger!.getBoundingClientRect()
      const box = bubble!.getBoundingClientRect()
      const width = document.documentElement.clientWidth
      const height = window.innerHeight
      const left = Math.max(12, Math.min(rect.left + (rect.width - box.width) / 2, width - box.width - 12))
      const top = rect.top >= box.height + 20 ? rect.top - box.height - 8 : Math.min(rect.bottom + 8, height - box.height - 12)
      bubble!.style.left = `${left}px`
      bubble!.style.top = `${Math.max(12, top)}px`
    }
    position()
    const resize = new ResizeObserver(position)
    resize.observe(trigger)
    resize.observe(bubble)
    const dismissOutside = (event: PointerEvent) => { if (!trigger.contains(event.target as Node)) hide() }
    const dismissOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault()
        event.stopPropagation()
        hide()
      }
    }
    window.addEventListener('resize', position)
    window.addEventListener('scroll', position, true)
    document.addEventListener('pointerdown', dismissOutside)
    document.addEventListener('keydown', dismissOnEscape, true)
    return () => {
      if (bubble.matches(':popover-open')) bubble.hidePopover()
      resize.disconnect()
      window.removeEventListener('resize', position)
      window.removeEventListener('scroll', position, true)
      document.removeEventListener('pointerdown', dismissOutside)
      document.removeEventListener('keydown', dismissOnEscape, true)
    }
  }, [open, disabled, disabledReason])

  // React can miss pointerenter when opening a modal makes the previous target
  // inert and Chrome only sends pointerover. Handle that event directly.
  return <span ref={anchor} className="button-tooltip" tabIndex={disabled ? 0 : undefined}
    role={disabled ? 'button' : undefined} aria-disabled={disabled || undefined}
    aria-label={disabled ? props['aria-label'] : undefined}
    aria-labelledby={disabled && !props['aria-label'] ? (props['aria-labelledby'] ?? buttonId) : undefined}
    aria-describedby={disabled ? [props['aria-describedby'], tooltipId].filter(Boolean).join(' ') : undefined}
    onPointerOver={event => { if (event.pointerType !== 'touch') show() }}
    onPointerLeave={event => {
      if (event.pointerType !== 'touch' && document.activeElement !== anchor.current) {
        clearTimeout(closeTimer.current)
        closeTimer.current = setTimeout(hide, 150)
      }
    }}
    onFocus={show} onBlur={hide}
    onClick={event => { if (disabled) { event.preventDefault(); event.stopPropagation(); show() } }}
    onKeyDown={event => {
      if (disabled && (event.key === 'Enter' || event.key === ' ')) {
        event.preventDefault()
        event.stopPropagation()
        show()
      }
    }}>
    <button {...props} id={buttonId} ref={button} disabled={disabled} aria-hidden={disabled || undefined}>{children}</button>
    {disabled && <span ref={tooltip} id={tooltipId} role="tooltip" popover="manual" className="button-tooltip__reason">
      {disabledReason}
    </span>}
  </span>
}
