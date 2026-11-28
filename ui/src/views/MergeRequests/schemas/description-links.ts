function isLocalHost(host: string) {
  if (host.startsWith('[')) {
    // WHATWG URL canonicalizes IPv6, including mapped IPv4 and alternative spellings.
    return !/^\[[23][0-9a-f]{3}:/u.test(host) || host.startsWith('[2001:db8:')
  }
  if (/^(?:\d+\.){3}\d+$/u.test(host)) {
    const [first = 0, second = 0, third = 0] = host.split('.').map(Number)
    return first === 0 || first === 10 || first === 127 || first >= 224
      || (first === 100 && second >= 64 && second <= 127)
      || (first === 169 && second === 254)
      || (first === 172 && second >= 16 && second <= 31)
      || (first === 192 && (second === 168 || (second === 0 && third === 0)))
      || (first === 198 && (second === 18 || second === 19))
  }
  return !host.includes('.') || /\.(?:localhost|localdomain|local|lan|internal|home\.arpa)$/u.test(host)
}

export function descriptionLink(href: string, workbenchHost: string) {
  if (!/^https?:\/\/[^/\\]/iu.test(href) || /[\\\u0000-\u001f\u007f]/u.test(href)) return undefined
  try {
    const url = new URL(href)
    const host = url.hostname.replace(/\.$/u, '')
    if (url.username || url.password || !host || host === workbenchHost.replace(/\.$/u, '') || isLocalHost(host)) {
      return undefined
    }
    if (/^\/\.yss-desktop(?:\/|$)/iu.test(decodeURIComponent(url.pathname))) return undefined
    return url.href
  } catch {
    return undefined
  }
}
