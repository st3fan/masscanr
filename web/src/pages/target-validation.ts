const maxEntries = 1024
const maxSelectorBytes = 64

function canonicalSelector(input: string): string | undefined {
  if (!input || new TextEncoder().encode(input).length > maxSelectorBytes) return undefined
  const as = /^as([0-9]+)$/i.exec(input)
  if (as) {
    const number = Number(as[1])
    return Number.isInteger(number) && number > 0 && number <= 0xffffffff
      ? `AS${number}`
      : undefined
  }
  const parts = input.split('/')
  if (parts.length > 2) return undefined
  const cidr = parts.length === 2
  const prefix = cidr ? parts[1] : '32'
  if (!/^(0|[1-9][0-9]*)$/.test(prefix)) return undefined
  const bits = Number(prefix)
  if (bits > 32) return undefined
  const octets = parts[0].split('.')
  if ((!cidr && octets.length !== 4) || octets.length < 1 || octets.length > 4) return undefined
  if (octets.some((part) => !/^(0|[1-9][0-9]*)$/.test(part) || Number(part) > 255)) return undefined
  while (octets.length < 4) octets.push('0')
  const address = octets.reduce((value, part) => ((value << 8) | Number(part)) >>> 0, 0)
  const mask = bits === 0 ? 0 : (0xffffffff << (32 - bits)) >>> 0
  const normalized = address & mask
  return `${[(normalized >>> 24) & 255, (normalized >>> 16) & 255, (normalized >>> 8) & 255, normalized & 255].join('.')}/${bits}`
}

export function validateTargetForm(name: string, entries: string[]) {
  const trimmedName = name.trim()
  const nameError =
    !trimmedName || new TextEncoder().encode(name).length > 200
      ? 'Name must contain 1–200 bytes.'
      : undefined
  const entryErrors: (string | undefined)[] = Array(entries.length).fill(undefined)
  const seen = new Map<string, number>()
  for (const [index, raw] of entries.entries()) {
    const selector = canonicalSelector(raw.trim())
    if (!selector) {
      entryErrors[index] = 'Enter an IPv4 address, CIDR, or AS number.'
    } else if (seen.has(selector)) {
      entryErrors[index] = `Duplicates selector ${seen.get(selector)! + 1}.`
    } else {
      seen.set(selector, index)
    }
  }
  const entriesError =
    entries.length < 1 || entries.length > maxEntries
      ? `Provide 1–${maxEntries} selectors.`
      : undefined
  return {
    name: trimmedName,
    entries: entries.map((entry) => entry.trim()),
    nameError,
    entriesError,
    entryErrors,
    valid: !nameError && !entriesError && entryErrors.every((error) => !error),
  }
}
