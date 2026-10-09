// Minimal IP / CIDR math for the form validators, mirroring the address
// checks in appliance/internal/config/validate.go (ParseCIDR, ParseWGKey and
// the subnet-membership checks). All values are plain strings at the UI
// boundary; these functions return null instead of throwing so forms can
// render errors declaratively.

/** A parsed IP address: family plus its value as a big-endian big integer. */
export interface IpAddress {
  readonly family: 4 | 6
  readonly value: bigint
}

/** A parsed CIDR: network base masked to the prefix length. */
export interface IpRange {
  readonly family: 4 | 6
  readonly base: bigint
  readonly prefix: number
}

/** Parse a dotted-quad IPv4 address; rejects leading zeros (as Go does). */
export function parseIPv4(s: string): bigint | null {
  const parts = s.split('.')
  if (parts.length !== 4) return null
  let value = 0n
  for (const part of parts) {
    if (!/^\d{1,3}$/.test(part)) return null
    if (part.length > 1 && part.startsWith('0')) return null
    const octet = Number(part)
    if (octet > 255) return null
    value = (value << 8n) | BigInt(octet)
  }
  return value
}

/** Parse an IPv6 address (with optional `::` compression and IPv4 tail). */
export function parseIPv6(s: string): bigint | null {
  let text = s
  let v4Tail: bigint | null = null
  const tail = /:(\d+\.\d+\.\d+\.\d+)$/.exec(text)
  if (tail !== null) {
    const tailText = tail[1] ?? ''
    v4Tail = parseIPv4(tailText)
    if (v4Tail === null) return null
    text = text.slice(0, -tailText.length)
  }

  const halves = text.split('::')
  if (halves.length > 2) return null

  const groupsOf = (part: string): readonly bigint[] | null => {
    if (part === '') return []
    const groups: bigint[] = []
    for (const g of part.split(':')) {
      if (!/^[0-9a-fA-F]{1,4}$/.test(g)) return null
      groups.push(BigInt(parseInt(g, 16)))
    }
    return groups
  }

  const left = halves[0] !== undefined ? groupsOf(halves[0]) : null
  const right = halves[1] !== undefined ? groupsOf(halves[1]) : null
  if (left === null || right === null) return null

  const explicit = left.length + right.length + (v4Tail !== null ? 2 : 0)
  const fill = halves.length === 2 ? 8 - explicit : 0
  if (fill < 0 || (halves.length === 1 && fill !== 0)) return null

  let value = 0n
  for (const g of left) value = (value << 16n) | g
  for (let i = 0; i < fill; i++) value <<= 16n
  for (const g of right) value = (value << 16n) | g
  if (v4Tail !== null) value = (value << 32n) | v4Tail
  return value
}

/** Parse an IP address of either family. */
export function parseIp(s: string): IpAddress | null {
  const trimmed = s.trim()
  const v4 = parseIPv4(trimmed)
  if (v4 !== null) return { family: 4, value: v4 }
  const v6 = parseIPv6(trimmed)
  if (v6 !== null) return { family: 6, value: v6 }
  return null
}

/** Parse an address in CIDR notation (a.b.c.d/nn or v6/prefix). */
export function parseCidr(s: string): IpRange | null {
  const idx = s.lastIndexOf('/')
  if (idx < 0) return null
  const addr = s.slice(0, idx)
  const prefixText = s.slice(idx + 1)
  if (!/^\d{1,3}$/.test(prefixText)) return null
  const prefix = Number(prefixText)

  const v4 = parseIPv4(addr)
  if (v4 !== null) {
    if (prefix > 32) return null
    return { family: 4, base: v4 & prefixMask(prefix, 32), prefix }
  }
  const v6 = parseIPv6(addr)
  if (v6 !== null) {
    if (prefix > 128) return null
    return { family: 6, base: v6 & prefixMask(prefix, 128), prefix }
  }
  return null
}

function prefixMask(prefix: number, bits: number): bigint {
  const all = (1n << BigInt(bits)) - 1n
  return (all << BigInt(bits - prefix)) & all
}

/** Whether `range` contains `ip` (same family, shared prefix bits). */
export function cidrContains(range: IpRange, ip: IpAddress): boolean {
  if (range.family !== ip.family) return false
  const bits = range.family === 4 ? 32 : 128
  return (ip.value & prefixMask(range.prefix, bits)) === range.base
}

/** Whether any subnet in `ranges` contains `ip`. */
export function inSubnets(ip: IpAddress, ranges: readonly IpRange[]): boolean {
  return ranges.some((range) => cidrContains(range, ip))
}
