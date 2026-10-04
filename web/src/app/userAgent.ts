/** A browser's User-Agent turned into what a person would call it. */
export function describeUserAgent(ua: string | undefined): { browser: string; system: string } {
  if (!ua) return { browser: '', system: '' }
  return { browser: browserOf(ua), system: systemOf(ua) }
}

function version(ua: string, token: string): string {
  const m = new RegExp(`${token}/(\\d+)`).exec(ua)
  return m ? ` ${m[1]}` : ''
}

function browserOf(ua: string): string {
  if (ua.includes('Edg/')) return `Edge${version(ua, 'Edg')}`
  if (ua.includes('OPR/')) return `Opera${version(ua, 'OPR')}`
  if (ua.includes('Firefox/')) return `Firefox${version(ua, 'Firefox')}`
  if (ua.includes('Chrome/')) return `Chrome${version(ua, 'Chrome')}`
  if (ua.includes('Safari/')) return `Safari${version(ua, 'Version')}`
  return ua.split(' ')[0] ?? ua
}

function systemOf(ua: string): string {
  if (/iPhone|iPad|iPod/.test(ua)) return 'iOS'
  if (ua.includes('Android')) return 'Android'
  if (ua.includes('CrOS')) return 'ChromeOS'
  if (ua.includes('Windows')) return 'Windows'
  if (ua.includes('Mac OS X')) return 'macOS'
  if (ua.includes('Linux')) return 'Linux'
  return ''
}
