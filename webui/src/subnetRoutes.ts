const invalid = () => new Error('Enter up to 64 unique subnet CIDRs with network addresses, such as 192.168.1.0/24 or fd00:1234::/64. Do not use default, loopback, link-local, multicast, or Tailscale ranges.')

function addressNumber(address: string): { value: bigint, bits: number, text: string } {
  if (!address.includes(':')) {
    const parts = address.split('.')
    if (parts.length !== 4 || parts.some(p => !/^(0|[1-9][0-9]{0,2})$/.test(p) || Number(p) > 255)) throw invalid()
    return { value: parts.reduce((v, p) => (v << 8n) + BigInt(p), 0n), bits: 32, text: address }
  }
  if (!/^[0-9a-fA-F:]+$/.test(address)) throw invalid()
  let text: string
  try { text = new URL('http://[' + address + ']/').hostname.slice(1, -1) } catch { throw invalid() }
  const halves = text.split('::')
  const left = halves[0] ? halves[0].split(':') : []
  const right = halves.length === 2 && halves[1] ? halves[1].split(':') : []
  const groups = halves.length === 2 ? [...left, ...Array(8-left.length-right.length).fill('0'), ...right] : left
  if (groups.length !== 8) throw invalid()
  return { value: groups.reduce((v, g) => (v << 16n) + BigInt('0x' + g), 0n), bits: 128, text }
}

const reserved = [
  '0.0.0.0/8', '127.0.0.0/8', '169.254.0.0/16', '224.0.0.0/4', '240.0.0.0/4', '100.64.0.0/10',
  '::/128', '::1/128', '::ffff:0:0/96', 'fe80::/10', 'ff00::/8', 'fd7a:115c:a1e0::/48',
].map(cidr => {
  const [address, prefix] = cidr.split('/')
  return { ...addressNumber(address), prefix: Number(prefix) }
})

export function canonicalSubnetRoutes(values: unknown): string[] {
  if (!Array.isArray(values) || values.length > 64) throw invalid()
  const result: string[] = []
  for (const route of values) {
    if (typeof route !== 'string') throw invalid()
    const match = /^([^/]+)\/([1-9][0-9]{0,2})$/.exec(route)
    if (!match) throw invalid()
    const address = addressNumber(match[1]), prefix = Number(match[2])
    if (prefix > address.bits) throw invalid()
    const hostBits = BigInt(address.bits-prefix)
    if ((address.value >> hostBits) << hostBits !== address.value) throw invalid()
    for (const blocked of reserved) {
      if (blocked.bits !== address.bits) continue
      const shift = BigInt(address.bits-Math.min(prefix, blocked.prefix))
      if (address.value >> shift === blocked.value >> shift) throw invalid()
    }
    const canonical = address.text + '/' + prefix
    if (result.includes(canonical)) throw invalid()
    result.push(canonical)
  }
  return result.sort()
}

export function parseSubnetRouteText(text: string): string[] {
  return canonicalSubnetRoutes(text.split(/[\n,]/).map(route => route.trim()).filter(Boolean))
}
