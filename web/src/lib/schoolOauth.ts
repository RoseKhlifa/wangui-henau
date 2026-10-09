export const HENAU_WECHAT_CALLBACK_EXAMPLE =
  'https://xhbcs.henau.edu.cn/%E5%86%9C%E5%A4%A7%E7%99%BD.png?code=EXAMPLE&state=EXAMPLE'

export type SchoolOauthInputKind = 'empty' | 'callback-url' | 'invalid'

export interface SchoolOauthInputDetection {
  kind: SchoolOauthInputKind
}

export function detectSchoolOauthInput(raw: string): SchoolOauthInputDetection {
  const value = raw.trim()
  if (!value) return { kind: 'empty' }
  try {
    const url = new URL(value)
    const codes = url.searchParams.getAll('code')
    const states = url.searchParams.getAll('state')
    const valid =
      url.protocol === 'https:' &&
      url.host === 'xhbcs.henau.edu.cn' &&
      decodeURIComponent(url.pathname) === '/农大白.png' &&
      codes.length === 1 &&
      states.length === 1 &&
      codes[0].trim().length > 0 &&
      states[0].length > 0
    return { kind: valid ? 'callback-url' : 'invalid' }
  } catch {
    return { kind: 'invalid' }
  }
}
