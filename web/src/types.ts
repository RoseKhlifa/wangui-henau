export interface TokenInfo {
  expiresAt: number
  validUntil: string
  remainingSec: number
  isValid: boolean
}

export interface SavedLocation {
  name: string
  latitude: number
  longitude: number
  address: string
  city: string
  road: string
  poi: string
}

export interface Settings {
  autoSign: boolean
  dormId: number | null
  dormName?: string
  latitude: number
  longitude: number
  address: string
  city: string
  road: string
  poi: string
  deviceModel: string
  deviceSystem: string
  triggerMinute: number
  jitterSec: number
  retryCount: number
  retryGapMin: number
  savedLocations: SavedLocation[]
  notifyEmail: string
  notifyEnabled: boolean
  // Server酱 (方糖) push channel — independent of email. The server never
  // returns the SendKey itself, only `serverChanKeySet` indicating whether
  // one is on file. To change it, send a non-empty `serverChanKey` value
  // in the next PUT /settings (empty means "keep existing").
  serverChanKey?: string // write-only field; server never echoes it back
  serverChanKeySet?: boolean
  serverChanEnabled?: boolean
  // 7-bit bitmask of which weekdays to auto-sign on.
  // bit 0 = Mon, bit 1 = Tue, … bit 5 = Sat, bit 6 = Sun. 127 = every day.
  signDays: number
  // YYYY-MM-DD list of dates the user has explicitly opted out of (e.g.
  // "我今晚不在校"). Scheduler skips these silently — no failure record.
  // Server prunes past dates whenever the list is updated.
  skipDates?: string[]
}

export interface SmtpConfig {
  enabled: boolean
  host: string
  port: number
  username: string
  from: string
  adminBcc: string
  passwordSet: boolean
  // Admin's global Server酱 push key (set+enabled = all sign results +
  // token-expiry warnings broadcast to admin's wechat). Key itself is
  // never returned by the API, only the `*KeySet` flag.
  adminServerChanKeySet: boolean
  adminServerChanEnabled: boolean
}

export interface SmtpUpdate {
  enabled: boolean
  host: string
  port: number
  username: string
  password: string // empty = keep current
  from: string
  adminBcc: string
  adminServerChanKey: string // empty = keep current
  adminServerChanEnabled: boolean
}

export interface Dorm {
  id: number
  name: string
  latitude: number
  longitude: number
  address: string
  city: string
  road: string
  poi: string
  note: string
  sendAddressFields: boolean
}

export interface AdminDorm extends Dorm {
  users: number
  createdAt: number
  updatedAt: number
}

export interface Me {
  userId: string
  userName: string
  userNumber: string
  userSection: string
  userClass: string
  userAvatarUrl: string
  inviteCode: string
  isDisabled: boolean
  hasPin: boolean
  token: TokenInfo
  settings: Settings
}

export type SignStatus = 'success' | 'already' | 'exempt' | 'failed' | 'skipped'

export interface SignRecord {
  id: number
  ruleId: number
  status: SignStatus
  message: string
  occurredAt: number
}

// Aggregate stats derived from a user's sign records. Computed on each
// /me/stats call; no DB counters to keep in sync.
export interface UserStats {
  currentStreak: number   // consecutive sign-days successfully signed up to today
  longestStreak: number   // best historical streak (within 365 days)
  monthSigned: number     // sign-days this month that were signed
  monthExpected: number   // sign-days this month so far (per signDays mask)
  monthRate: number       // monthSigned / monthExpected (0..1)
  totalSuccess: number
  totalAlready: number
  totalExempt: number
  totalFailed: number
  totalSkipped: number
  firstSignAt: number     // unix sec of oldest record we found; 0 if none
}

// ---- Admin types ----

export interface InviteCode {
  code: string
  boundUserId: string | null
  boundUserName: string
  boundAt: number | null
  note: string
  disabled: boolean
  createdAt: number
  createdBy: string
  used: boolean
}

export interface AdminUser {
  userId: string
  userName: string
  userNumber: string
  userSection: string
  userClass: string
  userAvatarUrl: string
  inviteCode: string
  isDisabled: boolean
  autoSign: boolean
  latitude: number
  longitude: number
  dormId?: number | null
  dormName?: string
  tokenExp: number
  tokenValid: boolean
  createdAt: number
  updatedAt: number
  signDays: number
  triggerMinute: number
  jitterSec: number
  recentRecords?: Array<{
    id: number
    status: SignStatus
    message: string
    occurredAt: number
  }>
}

// Snapshot of the school's "what should this user do tonight" answer.
// `state` is a coarse bucket derived server-side from the school's response.
export interface SchoolCheckinStatus {
  state: 'pending' | 'canSign' | 'signed' | 'exempt' | 'boarding' | 'tokenExpired' | 'error'
  message: string
  canCheckin?: boolean
  hasCheckedIn?: boolean | null
  isExempt?: boolean | null
  isBoarding?: boolean
  exemptReason?: string | null
  currentRule?: {
    ruleId: number
    ruleName: string
    startTime: string
    endTime: string
    description: string
  }
}

export interface DormUserBrief {
  userId: string
  userName: string
  userNumber: string
  userSection: string
  userClass: string
  isDisabled: boolean
  autoSign: boolean
}

export interface AdminStats {
  users: { total: number; guests: number; disabled: number; expiring: number }
  codes: { total: number; used: number; unused: number }
  today: Record<string, number>
}

export interface AdminGuest {
  userId: string
  userName: string
  userNumber: string
  userSection: string
  userClass: string
  userAvatarUrl: string
  label: string
  signDates: string[]
  tokenExp: number
  tokenValid: boolean
  createdAt: number
  dormId?: number | null
  dormName?: string
  expiresAt: number | null
  autoSign: boolean
  isDisabled: boolean
  triggerMinute: number
  jitterSec: number
  recentRecords?: Array<{
    id: number
    status: SignStatus
    message: string
    occurredAt: number
  }>
}

export interface GuestCreateReq {
  label: string
  signDates: string[]
  dormId?: number
  callbackUrl?: string
  oauthAttemptId?: string
  token?: string
}

export interface GuestUpdateReq {
  label?: string
  signDates?: string[]
}

export interface AdminLog {
  id: number
  userId: string
  userName: string
  status: SignStatus
  message: string
  occurredAt: number
}

export type AnnouncementLevel = 'info' | 'success' | 'warning' | 'critical'

// Admin-authored notice displayed on the user Dashboard. The plain-text
// content is rendered with a tiny inline markdown subset (newlines,
// **bold**, *italic*, [link](url)) — no raw HTML.
export interface Announcement {
  id: number
  title: string
  content: string
  level: AnnouncementLevel
  expiresAt: number | null
  createdAt: number
  updatedAt: number
}

export interface AnnouncementUpsertReq {
  title: string
  content: string
  level: AnnouncementLevel
  expiresAt?: number | null
}
