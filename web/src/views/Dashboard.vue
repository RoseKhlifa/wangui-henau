<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import {
  CheckCircle2,
  XCircle,
  AlertTriangle,
  Clock,
  Zap,
  ScrollText,
  ArrowRight,
  KeyRound,
  MapPin,
  Bell,
  BellOff,
  Flame,
  CalendarCheck,
  TrendingUp,
  Trophy,
  ExternalLink,
  CalendarX,
  CalendarCheck2,
} from 'lucide-vue-next'
import type { SignRecord, UserStats } from '../types'
import { useAuth } from '../stores/auth'
import { api } from '../api'
import { formatDateTime, formatRemaining } from '../lib/format'
import { showToast } from '../lib/toast'
import Avatar from '../components/Avatar.vue'

const auth = useAuth()
const records = ref<SignRecord[]>([])
const stats = ref<UserStats | null>(null)
const now = ref(new Date())
const signing = ref(false)

let timer: number | null = null
onMounted(async () => {
  await auth.init()
  await Promise.all([loadRecords(), loadStats()])
  // Tick every 30s. During the 22:00–22:35 window, also re-pull records
  // so the "今日签到" status auto-transitions from "正在尝试" → "已完成"
  // without the user having to refresh.
  timer = window.setInterval(async () => {
    now.value = new Date()
    const h = now.value.getHours()
    const m = now.value.getMinutes()
    if (h === 22 && m <= 35) {
      await Promise.all([loadRecords(), loadStats()])
    }
  }, 30_000)
  // Announcements polling now lives in UserLayout so the notices follow
  // the user across every page in the user area.
})
onUnmounted(() => {
  if (timer) clearInterval(timer)
})

async function loadRecords() {
  try {
    records.value = await api.records()
  } catch {
    records.value = []
  }
}

async function loadStats() {
  try {
    stats.value = await api.stats()
  } catch {
    stats.value = null
  }
}

// Visual tone for the streak chip based on current streak length.
const streakTone = computed(() => {
  const n = stats.value?.currentStreak ?? 0
  if (n >= 30) return 'fire' // amber/red — on fire
  if (n >= 7) return 'good'  // emerald — solid
  if (n >= 1) return 'mild'  // blue — getting there
  return 'cold'              // zinc — nothing yet
})

// Compact percentage display for monthly progress.
const monthPct = computed(() => {
  const s = stats.value
  if (!s || s.monthExpected === 0) return 0
  return Math.round(s.monthRate * 100)
})

async function refreshStats() {
  // Triggered after manual sign so the streak/月统计 react instantly.
  await loadStats()
}

const me = computed(() => auth.state.me)

function greeting(): string {
  const h = new Date().getHours()
  if (h < 6) return '夜深了'
  if (h < 12) return '早上好'
  if (h < 14) return '中午好'
  if (h < 18) return '下午好'
  if (h < 22) return '晚上好'
  return '夜深了'
}

const tokenValid = computed(
  () => !!me.value?.token.isValid && me.value!.token.remainingSec > 0,
)
const tokenColor = computed(() => {
  if (!me.value) return 'text-zinc-500 dark:text-zinc-400'
  const s = me.value.token.remainingSec
  if (s <= 0) return 'text-red-400'
  if (s < 24 * 3600) return 'text-red-400'
  if (s < 3 * 24 * 3600) return 'text-amber-400'
  return 'text-emerald-400'
})

// Today's most recent record (any status). Used by the Today-status card
// below to decide which terminal state to render.
const todayRecord = computed(() => {
  const t = new Date()
  const y = t.getFullYear()
  const m = t.getMonth()
  const d = t.getDate()
  for (const r of records.value) {
    const ts = new Date(r.occurredAt * 1000)
    if (ts.getFullYear() === y && ts.getMonth() === m && ts.getDate() === d) {
      return r
    }
  }
  return null
})

// Is today a sign-in day per the user's sign_days bitmask?
// JS getDay(): 0=Sun..6=Sat. We map to bit 0=Mon..bit 6=Sun.
const isSignDayToday = computed(() => {
  const sd = me.value?.settings.signDays
  if (typeof sd !== 'number') return true // legacy users: default every day
  if ((sd & 0x7f) === 0) return false
  const bit = (new Date().getDay() + 6) % 7
  return (sd & (1 << bit)) !== 0
})

// Personal sign moment: 22:00 + my triggerMinute. Each user has their own
// trigger so they don't all fire at the same second. Displayed to the user
// so they know when to expect the email rather than refreshing from 21:50.
const mySignMinute = computed(() => me.value?.settings.triggerMinute ?? 2)

const mySignTimeStr = computed(() => `22:${String(mySignMinute.value).padStart(2, '0')}`)

// Countdown to the NEXT firing of my sign moment. Always points to a future
// time — if today's moment has passed, this counts to tomorrow's. Used by
// the "我的签到时刻" card to render a stable schedule view.
const untilMySign = computed(() => {
  const n = now.value
  const target = new Date(n)
  target.setHours(22, mySignMinute.value, 0, 0)
  if (target <= n) target.setDate(target.getDate() + 1)
  const ms = target.getTime() - n.getTime()
  const totalMin = Math.floor(ms / 60000)
  return { h: Math.floor(totalMin / 60), m: totalMin % 60, totalMin }
})

const todayDateStr = computed(() => {
  const d = new Date()
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
})
const isSkippedToday = computed(() =>
  Array.isArray(me.value?.settings.skipDates) &&
  me.value!.settings.skipDates!.includes(todayDateStr.value),
)
const skippingToday = ref(false)

async function toggleSkipToday() {
  if (skippingToday.value) return
  const turningOn = !isSkippedToday.value
  if (turningOn && !confirm(
    '确定今晚不在校吗？\n\n勾上后 wangui 不会替你签到 —— 防止「人不在校但脚本签到」=谎报位置=违纪。\n\n再点一次可以取消。',
  )) {
    return
  }
  skippingToday.value = true
  try {
    await api.skipToday()
    await auth.refresh()
    showToast('ok', turningOn ? '今晚已跳过 · 不会自动签' : '已恢复今晚自动签')
  } catch (e: any) {
    showToast('err', e.message || '操作失败')
  } finally {
    skippingToday.value = false
  }
}

// Notification-channel summary, surfaced as a chip in the hero so the user
// remembers what they will/won't get pinged on. Reads only the boolean flags
// + the keySet marker — the actual SendKey never reaches the browser.
type NotifyTone = 'emerald' | 'amber' | 'zinc'
const notifyState = computed<{ tone: NotifyTone; label: string; sub: string }>(() => {
  const s = me.value?.settings
  if (!s) return { tone: 'zinc', label: '通知未配置', sub: '前往配置 →' }
  const emailOn = !!s.notifyEnabled && !!s.notifyEmail
  const wechatOn = !!s.serverChanEnabled && !!s.serverChanKeySet
  const emailConfigured = !!s.notifyEmail
  const wechatConfigured = !!s.serverChanKeySet
  if (emailOn && wechatOn) return { tone: 'emerald', label: '通知已开启', sub: '微信 + 邮件' }
  if (wechatOn) return { tone: 'emerald', label: '通知已开启', sub: '仅微信' }
  if (emailOn) return { tone: 'emerald', label: '通知已开启', sub: '仅邮件' }
  if (emailConfigured || wechatConfigured) return { tone: 'amber', label: '通知已关闭', sub: '已配置但未启用' }
  return { tone: 'zinc', label: '通知未配置', sub: '前往配置 →' }
})

// State machine for the "今日签到" status card. Each branch is exclusive.
// Order matters: a record observed today always wins over time-based guesses.
//   resting   — today is not in signDays (no firing today)
//   done      — todayRecord exists with success/already
//   exempt    — todayRecord exists with status=exempt (请假 / 节假日 / 走读)
//   failed    — todayRecord exists with status=failed (retryable via 立即签到)
//   missed    — past 22:30 today, no successful record
//   trying    — within 22:00–22:30, no record yet, my sign moment reached
//   imminent  — within 22:00–22:30, no record yet, my sign moment not yet
//   waiting   — anything else (most of the day before 22:00 / next-day)
type TodayState = 'resting' | 'done' | 'exempt' | 'failed' | 'missed' | 'trying' | 'imminent' | 'waiting'
const todayState = computed<TodayState>(() => {
  if (!isSignDayToday.value) return 'resting'
  const rec = todayRecord.value
  if (rec) {
    if (rec.status === 'success' || rec.status === 'already') return 'done'
    if (rec.status === 'exempt') return 'exempt'
    if (rec.status === 'failed') return 'failed'
    // 'skipped' falls through to time-based logic
  }
  const n = now.value
  const h = n.getHours()
  const m = n.getMinutes()
  if (h === 22 && m < 30) {
    return m >= mySignMinute.value ? 'trying' : 'imminent'
  }
  if (h > 22 || (h === 22 && m >= 30)) return 'missed'
  return 'waiting'
})

async function signNow() {
  if (signing.value || !me.value) return
  signing.value = true
  try {
    const res = await api.signNow()
    if (res.status === 'success') showToast('ok', '签到成功 🎉')
    else if (res.status === 'already') showToast('ok', '今日已签到')
    else if (res.status === 'exempt') showToast('ok', res.message || '免签')
    else showToast('err', res.message || '签到失败')
    await loadRecords()
    await refreshStats()
    await auth.refresh()
  } catch (e: any) {
    showToast('err', e.message || '签到失败')
  } finally {
    signing.value = false
  }
}

const recentRecords = computed(() => records.value.slice(0, 5))

const recordMeta: Record<string, { label: string; color: string; dotBg: string }> = {
  success: { label: '签到成功', color: 'text-emerald-400', dotBg: 'bg-emerald-500' },
  already: { label: '今日已签', color: 'text-blue-400', dotBg: 'bg-blue-500' },
  exempt: { label: '免签', color: 'text-zinc-500 dark:text-zinc-400', dotBg: 'bg-zinc-500' },
  failed: { label: '签到失败', color: 'text-red-400', dotBg: 'bg-red-500' },
  skipped: { label: '跳过', color: 'text-amber-400', dotBg: 'bg-amber-500' },
}
</script>

<template>
  <div v-if="me" class="space-y-3">
    <!-- Hero -->
    <section
      class="relative overflow-hidden rounded-2xl bg-gradient-to-br from-white to-zinc-100/40 dark:from-zinc-900 dark:to-zinc-900/40 ring-1 ring-black/[0.08] dark:ring-white/[0.06] p-5 ambient-glow"
    >
      <div class="relative flex items-center gap-4">
        <Avatar
          :src="me.userAvatarUrl"
          :name="me.userName"
          :size="56"
          rounded="xl"
        />
        <div class="min-w-0 flex-1">
          <p class="text-[11px] text-zinc-500 tracking-wide uppercase">{{ greeting() }}</p>
          <h2 class="text-xl font-bold tracking-tight mt-0.5 truncate">
            欢迎回来,{{ me.userName }}
          </h2>
          <p class="text-sm text-zinc-500 dark:text-zinc-400 mt-1 truncate">
            <span>{{ me.userSection || '—' }}</span>
            <span class="mx-1.5 text-zinc-400 dark:text-zinc-700">·</span>
            <span>{{ me.userClass || '—' }}</span>
            <span class="mx-1.5 text-zinc-400 dark:text-zinc-700">·</span>
            <span class="font-mono-token text-zinc-500">{{ me.userNumber }}</span>
          </p>
        </div>
        <!-- Right-side status chips: dorm + notification channels.
             Stacked vertically on desktop, hidden here on mobile and
             re-shown below as a row for tighter vertical use of space. -->
        <div class="hidden sm:flex flex-col gap-1.5 shrink-0">
          <RouterLink
            to="/settings"
            class="flex items-center gap-2 px-3 py-2 rounded-lg ring-1 transition-colors text-xs"
            :class="me.settings.dormName
              ? 'bg-emerald-500/10 ring-emerald-500/25 text-emerald-300 hover:bg-emerald-500/15'
              : 'bg-amber-500/10 ring-amber-500/25 text-amber-300 hover:bg-amber-500/15'"
          >
            <MapPin class="w-3.5 h-3.5" />
            <div class="leading-tight">
              <div class="text-[10px] opacity-70 tracking-wide uppercase">
                {{ me.settings.dormName ? '已绑定位置' : '未绑定位置' }}
              </div>
              <div class="font-medium truncate max-w-[160px]">
                {{ me.settings.dormName || '前往配置 →' }}
              </div>
            </div>
          </RouterLink>
          <RouterLink
            to="/settings"
            class="flex items-center gap-2 px-3 py-2 rounded-lg ring-1 transition-colors text-xs"
            :class="notifyState.tone === 'emerald'
              ? 'bg-emerald-500/10 ring-emerald-500/25 text-emerald-300 hover:bg-emerald-500/15'
              : notifyState.tone === 'amber'
                ? 'bg-amber-500/10 ring-amber-500/25 text-amber-300 hover:bg-amber-500/15'
                : 'bg-zinc-500/10 ring-zinc-500/25 text-zinc-500 hover:bg-zinc-500/15'"
            :title="notifyState.tone === 'emerald'
              ? '签到结果 + Token 即将过期都会推到这些通道'
              : notifyState.tone === 'amber'
                ? '已配置但开关都关着，不会收到提醒'
                : '没有任何通知通道；签到出错你不会知道'"
          >
            <Bell v-if="notifyState.tone === 'emerald'" class="w-3.5 h-3.5" />
            <BellOff v-else class="w-3.5 h-3.5" />
            <div class="leading-tight">
              <div class="text-[10px] opacity-70 tracking-wide uppercase">
                {{ notifyState.label }}
              </div>
              <div class="font-medium truncate max-w-[160px]">
                {{ notifyState.sub }}
              </div>
            </div>
          </RouterLink>
        </div>
      </div>
      <!-- Mobile-only status row -->
      <div class="sm:hidden mt-3 grid grid-cols-2 gap-2">
        <RouterLink
          to="/settings"
          class="flex items-center gap-1.5 px-2.5 py-2 rounded-lg ring-1 text-[11px] min-w-0"
          :class="me.settings.dormName
            ? 'bg-emerald-500/10 ring-emerald-500/25 text-emerald-300'
            : 'bg-amber-500/10 ring-amber-500/25 text-amber-300'"
        >
          <MapPin class="w-3.5 h-3.5 shrink-0" />
          <span class="font-medium truncate">
            {{ me.settings.dormName || '未绑定位置' }}
          </span>
        </RouterLink>
        <RouterLink
          to="/settings"
          class="flex items-center gap-1.5 px-2.5 py-2 rounded-lg ring-1 text-[11px] min-w-0"
          :class="notifyState.tone === 'emerald'
            ? 'bg-emerald-500/10 ring-emerald-500/25 text-emerald-300'
            : notifyState.tone === 'amber'
              ? 'bg-amber-500/10 ring-amber-500/25 text-amber-300'
              : 'bg-zinc-500/10 ring-zinc-500/25 text-zinc-500'"
        >
          <Bell v-if="notifyState.tone === 'emerald'" class="w-3.5 h-3.5 shrink-0" />
          <BellOff v-else class="w-3.5 h-3.5 shrink-0" />
          <span class="font-medium truncate">{{ notifyState.sub }}</span>
        </RouterLink>
      </div>
    </section>

    <!-- Stats strip (streak + 月度 + 累计) -->
    <section
      v-if="stats"
      class="grid grid-cols-2 sm:grid-cols-4 gap-2"
    >
      <!-- Current streak — tone shifts as the user gets hotter. -->
      <div
        class="rounded-xl ring-1 p-3"
        :class="streakTone === 'fire'
          ? 'bg-gradient-to-br from-amber-500/15 to-red-500/10 ring-amber-500/30'
          : streakTone === 'good'
            ? 'bg-emerald-500/10 ring-emerald-500/25'
            : streakTone === 'mild'
              ? 'bg-blue-500/10 ring-blue-500/25'
              : 'bg-white/85 dark:bg-zinc-900/60 ring-black/[0.08] dark:ring-white/[0.06]'"
      >
        <div class="flex items-center gap-1.5 mb-1">
          <Flame
            class="w-3.5 h-3.5"
            :class="streakTone === 'fire' ? 'text-amber-400' : streakTone === 'good' ? 'text-emerald-400' : streakTone === 'mild' ? 'text-blue-400' : 'text-zinc-500'"
          />
          <span class="text-[10px] uppercase tracking-wide text-zinc-500">连签</span>
        </div>
        <div class="flex items-baseline gap-1">
          <span
            class="text-2xl font-bold tabular-nums"
            :class="streakTone === 'fire' ? 'text-amber-400' : streakTone === 'good' ? 'text-emerald-400' : streakTone === 'mild' ? 'text-blue-400' : 'text-zinc-500'"
          >
            {{ stats.currentStreak }}
          </span>
          <span class="text-xs text-zinc-500">天</span>
        </div>
        <p class="text-[10px] text-zinc-500 mt-1 truncate" :title="`历史最长 ${stats.longestStreak} 天`">
          最长 {{ stats.longestStreak }} 天
        </p>
      </div>

      <!-- Month progress -->
      <div class="rounded-xl bg-white/85 dark:bg-zinc-900/60 ring-1 ring-black/[0.08] dark:ring-white/[0.06] p-3">
        <div class="flex items-center gap-1.5 mb-1">
          <CalendarCheck class="w-3.5 h-3.5 text-zinc-500" />
          <span class="text-[10px] uppercase tracking-wide text-zinc-500">本月</span>
        </div>
        <div class="flex items-baseline gap-1">
          <span class="text-2xl font-bold tabular-nums">{{ stats.monthSigned }}</span>
          <span class="text-xs text-zinc-500">/ {{ stats.monthExpected }}</span>
        </div>
        <div class="mt-1.5 h-1 rounded-full bg-zinc-200 dark:bg-zinc-800 overflow-hidden">
          <div
            class="h-full bg-emerald-500 transition-[width] duration-500"
            :style="`width: ${monthPct}%`"
          />
        </div>
        <p class="text-[10px] text-zinc-500 mt-1 tabular-nums">{{ monthPct }}% 已完成</p>
      </div>

      <!-- Lifetime total -->
      <div class="rounded-xl bg-white/85 dark:bg-zinc-900/60 ring-1 ring-black/[0.08] dark:ring-white/[0.06] p-3">
        <div class="flex items-center gap-1.5 mb-1">
          <TrendingUp class="w-3.5 h-3.5 text-zinc-500" />
          <span class="text-[10px] uppercase tracking-wide text-zinc-500">总签到</span>
        </div>
        <div class="flex items-baseline gap-1">
          <span class="text-2xl font-bold tabular-nums">
            {{ stats.totalSuccess + stats.totalAlready }}
          </span>
          <span class="text-xs text-zinc-500">次</span>
        </div>
        <p class="text-[10px] text-zinc-500 mt-1 truncate" :title="`成功 ${stats.totalSuccess} · 已签 ${stats.totalAlready} · 失败 ${stats.totalFailed}`">
          <span v-if="stats.totalExempt > 0">+{{ stats.totalExempt }} 免签 · </span>
          <span class="text-red-400" v-if="stats.totalFailed > 0">{{ stats.totalFailed }} 失败</span>
          <span v-else>0 失败</span>
        </p>
      </div>

      <!-- Best ever -->
      <div class="rounded-xl bg-white/85 dark:bg-zinc-900/60 ring-1 ring-black/[0.08] dark:ring-white/[0.06] p-3">
        <div class="flex items-center gap-1.5 mb-1">
          <Trophy class="w-3.5 h-3.5 text-zinc-500" />
          <span class="text-[10px] uppercase tracking-wide text-zinc-500">最佳记录</span>
        </div>
        <div class="flex items-baseline gap-1">
          <span class="text-2xl font-bold tabular-nums">{{ stats.longestStreak }}</span>
          <span class="text-xs text-zinc-500">天</span>
        </div>
        <p class="text-[10px] text-zinc-500 mt-1 truncate">
          <template v-if="stats.currentStreak >= stats.longestStreak && stats.currentStreak > 0">
            🔥 当前正在刷新记录
          </template>
          <template v-else-if="stats.longestStreak > 0">
            历史最长连签
          </template>
          <template v-else>
            等你第一次破纪录
          </template>
        </p>
      </div>
    </section>

    <!-- KPI row -->
    <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
      <!--
        Today status card — single source of truth for "今天签了吗".
        Drives off todayState, which observes the actual record. After the
        system fires at 22:0X, the record arrives, records auto-poll picks
        it up within 30s, and this card flips to "已完成" on its own.
      -->
      <div class="rounded-xl bg-white/85 dark:bg-zinc-900/60 ring-1 ring-black/[0.08] dark:ring-white/[0.06] p-4">
        <div class="flex items-center gap-1.5 mb-2">
          <CheckCircle2 class="w-3.5 h-3.5 text-zinc-500" />
          <span class="text-[11px] text-zinc-500 tracking-wide uppercase">今日签到</span>
        </div>
        <template v-if="todayState === 'resting'">
          <div class="flex items-center gap-2">
            <span class="w-2 h-2 rounded-full bg-zinc-500" />
            <span class="text-zinc-500 dark:text-zinc-400 font-semibold text-base">今日休息</span>
          </div>
          <p class="text-xs text-zinc-500 mt-1.5">不在你设置的签到日期内</p>
        </template>
        <template v-else-if="todayState === 'done'">
          <div class="flex items-center gap-2">
            <span class="w-2 h-2 rounded-full bg-emerald-500 shadow-[0_0_8px_rgb(16_185_129_/_0.8)]" />
            <span class="text-emerald-400 font-semibold text-base">已完成</span>
          </div>
          <p class="text-xs text-zinc-500 mt-1.5">
            {{ todayRecord?.status === 'already' ? '今日已签到 ✓' : '签到成功 ✓' }}
            <span v-if="todayRecord" class="ml-1 text-zinc-500 dark:text-zinc-600">
              {{ formatDateTime(todayRecord.occurredAt).slice(11, 16) }}
            </span>
          </p>
        </template>
        <template v-else-if="todayState === 'exempt'">
          <div class="flex items-center gap-2">
            <span class="w-2 h-2 rounded-full bg-blue-500" />
            <span class="text-blue-400 font-semibold text-base">今日免签</span>
          </div>
          <p class="text-xs text-zinc-500 mt-1.5 truncate">
            {{ todayRecord?.message || '学校系统标记免签（请假 / 节假日 / 走读）' }}
          </p>
        </template>
        <template v-else-if="todayState === 'failed'">
          <div class="flex items-center gap-2">
            <span class="w-2 h-2 rounded-full bg-red-500 shadow-[0_0_8px_rgb(239_68_68_/_0.8)]" />
            <span class="text-red-400 font-semibold text-base">签到失败</span>
          </div>
          <p class="text-xs text-zinc-500 mt-1.5 truncate">
            {{ todayRecord?.message || '执行失败，请点「立即签到」重试' }}
          </p>
        </template>
        <template v-else-if="todayState === 'missed'">
          <div class="flex items-center gap-2">
            <span class="w-2 h-2 rounded-full bg-red-500" />
            <span class="text-red-400 font-semibold text-base">已错过窗口</span>
          </div>
          <p class="text-xs text-zinc-500 mt-1.5">22:30 已过且无签到记录，请检查 Token / 配置</p>
        </template>
        <template v-else-if="todayState === 'trying'">
          <div class="flex items-center gap-2">
            <span class="w-2 h-2 rounded-full bg-amber-500 shadow-[0_0_8px_rgb(245_158_11_/_0.8)] animate-pulse" />
            <span class="text-amber-400 font-semibold text-base">正在尝试…</span>
          </div>
          <p class="text-xs text-zinc-500 mt-1.5">后台正在执行，30 秒内自动刷新</p>
        </template>
        <template v-else-if="todayState === 'imminent'">
          <div class="flex items-center gap-2">
            <span class="w-2 h-2 rounded-full bg-amber-500" />
            <span class="text-amber-400 font-semibold text-base">即将开始</span>
          </div>
          <p class="text-xs text-zinc-500 mt-1.5">
            还差 {{ Math.max(0, mySignMinute - now.getMinutes()) }} 分钟到你的预定时刻
          </p>
        </template>
        <template v-else>
          <div class="flex items-center gap-2">
            <span class="w-2 h-2 rounded-full bg-zinc-500" />
            <span class="text-zinc-500 dark:text-zinc-400 font-semibold text-base">待签到</span>
          </div>
          <p class="text-xs text-zinc-500 mt-1.5">今晚 22:00–22:30 自动触发</p>
        </template>
      </div>

      <!--
        Sign-time card — pure schedule info. NEVER reflects today's state.
        Always shows the configured 22:0X and a countdown to the next
        occurrence. Keeps semantics distinct from the Today card so the
        two never tell the same story.
      -->
      <div class="rounded-xl bg-white/85 dark:bg-zinc-900/60 ring-1 ring-black/[0.08] dark:ring-white/[0.06] p-4">
        <div class="flex items-center gap-1.5 mb-2">
          <Clock class="w-3.5 h-3.5 text-zinc-500" />
          <span class="text-[11px] text-zinc-500 tracking-wide uppercase">我的签到时刻</span>
        </div>
        <div class="flex items-baseline gap-1.5">
          <span class="text-3xl font-bold tabular-nums leading-none text-emerald-400">
            {{ mySignTimeStr }}
          </span>
          <span class="text-[10px] text-zinc-500">±60秒</span>
        </div>
        <p class="text-xs text-zinc-500 mt-1.5">
          每天此刻自动签 ·
          <template v-if="untilMySign.totalMin < 60">
            {{ untilMySign.totalMin <= 0 ? '刚刚' : '还有 ' + untilMySign.m + ' 分钟' }}
          </template>
          <template v-else>
            距下次 {{ untilMySign.h }} 小时{{ untilMySign.m > 0 ? ' ' + untilMySign.m + ' 分钟' : '' }}
          </template>
        </p>
      </div>

      <!-- Token -->
      <RouterLink
        to="/account"
        class="rounded-xl bg-white/85 dark:bg-zinc-900/60 ring-1 ring-black/[0.08] dark:ring-white/[0.06] p-4 hover:ring-black/[0.12] dark:hover:ring-white/[0.12] transition-all block"
      >
        <div class="flex items-center gap-1.5 mb-2">
          <KeyRound class="w-3.5 h-3.5 text-zinc-500" />
          <span class="text-[11px] text-zinc-500 tracking-wide uppercase">Token</span>
        </div>
        <div class="flex items-center gap-2">
          <CheckCircle2 v-if="tokenValid" class="w-4 h-4" :class="tokenColor" />
          <XCircle v-else class="w-4 h-4 text-red-400" />
          <span class="font-semibold text-base" :class="tokenColor">
            {{ tokenValid ? '有效' : '已失效' }}
          </span>
        </div>
        <p class="text-xs text-zinc-500 mt-1.5">
          {{ formatRemaining(me.token.remainingSec) }}
        </p>
      </RouterLink>
    </div>

    <!-- Action row — 4 buttons: 立即签到 / 今晚跳过 / 配置 / 跳学校 H5 -->
    <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3">
      <!-- 立即签到 -->
      <button
        @click="signNow"
        :disabled="signing"
        class="group rounded-xl bg-emerald-500/10 hover:bg-emerald-500/15 ring-1 ring-emerald-500/30 hover:ring-emerald-500/50 p-4 text-left transition-all disabled:opacity-50"
      >
        <div class="flex items-start justify-between mb-2">
          <Zap class="w-5 h-5 text-emerald-400" :class="signing ? 'wangui-spin' : ''" />
          <ArrowRight class="w-4 h-4 text-emerald-400/60 group-hover:translate-x-0.5 transition-transform" />
        </div>
        <p class="font-semibold text-emerald-200 text-base">立即签到</p>
        <p class="text-xs text-emerald-400/70 mt-0.5">在窗口期内手动触发一次</p>
      </button>

      <!-- Z 今晚跳过 / 恢复 -->
      <button
        @click="toggleSkipToday"
        :disabled="skippingToday"
        class="group rounded-xl ring-1 p-4 text-left transition-all disabled:opacity-50"
        :class="isSkippedToday
          ? 'bg-amber-500/10 hover:bg-amber-500/15 ring-amber-500/30 hover:ring-amber-500/50'
          : 'bg-white/85 dark:bg-zinc-900/60 hover:bg-zinc-100 dark:hover:bg-zinc-900 ring-black/[0.08] dark:ring-white/[0.06] hover:ring-amber-500/30'"
        :title="isSkippedToday ? '点击恢复今晚自动签' : '今晚不在校时点这里，wangui 会跳过签到，避免谎报位置'"
      >
        <div class="flex items-start justify-between mb-2">
          <component
            :is="isSkippedToday ? CalendarX : CalendarCheck2"
            class="w-5 h-5"
            :class="isSkippedToday ? 'text-amber-400' : 'text-zinc-500 dark:text-zinc-400'"
          />
          <ArrowRight class="w-4 h-4 group-hover:translate-x-0.5 transition-transform"
            :class="isSkippedToday ? 'text-amber-400/60' : 'text-zinc-500/60'" />
        </div>
        <p class="font-semibold text-base" :class="isSkippedToday ? 'text-amber-300' : ''">
          {{ isSkippedToday ? '今晚已跳过' : '今晚不在校' }}
        </p>
        <p class="text-xs mt-0.5" :class="isSkippedToday ? 'text-amber-400/70' : 'text-zinc-500'">
          {{ isSkippedToday ? '点击恢复 · 今晚不签' : '点这里 · 今晚不替你签到' }}
        </p>
      </button>

      <!-- 配置 -->
      <RouterLink
        to="/settings"
        class="group rounded-xl bg-white/85 dark:bg-zinc-900/60 hover:bg-zinc-100 dark:hover:bg-zinc-900 ring-1 ring-black/[0.08] dark:ring-white/[0.06] hover:ring-black/[0.12] dark:hover:ring-white/[0.12] p-4 text-left transition-all block"
      >
        <div class="flex items-start justify-between mb-2">
          <MapPin class="w-5 h-5 text-zinc-500 dark:text-zinc-400" />
          <ArrowRight class="w-4 h-4 text-zinc-500 group-hover:translate-x-0.5 transition-transform" />
        </div>
        <p class="font-semibold text-base">打卡位置 / 配置</p>
        <p class="text-xs text-zinc-500 mt-0.5">
          {{ me.settings.latitude !== 0 ? '已配置' : '⚠ 未配置坐标' }}
        </p>
      </RouterLink>

      <!-- AF 跳学校 H5 (兜底通道) -->
      <a
        href="https://xhbcs.henau.edu.cn"
        target="_blank"
        rel="noopener noreferrer"
        class="group rounded-xl bg-white/85 dark:bg-zinc-900/60 hover:bg-blue-500/[0.08] ring-1 ring-black/[0.08] dark:ring-white/[0.06] hover:ring-blue-500/40 p-4 text-left transition-all block"
        title="跳学校晚归 H5（应急 / 手动签到通道）"
      >
        <div class="flex items-start justify-between mb-2">
          <ExternalLink class="w-5 h-5 text-blue-400" />
          <ArrowRight class="w-4 h-4 text-zinc-500 group-hover:translate-x-0.5 transition-transform" />
        </div>
        <p class="font-semibold text-base">学校晚归页</p>
        <p class="text-xs text-zinc-500 mt-0.5">应急 · 手动签到通道</p>
      </a>
    </div>

    <!-- Recent records preview -->
    <section class="rounded-xl bg-white/85 dark:bg-zinc-900/60 ring-1 ring-black/[0.08] dark:ring-white/[0.06] p-5">
      <div class="flex items-center justify-between mb-3">
        <div class="flex items-center gap-2">
          <ScrollText class="w-4 h-4 text-zinc-500" />
          <h3 class="text-base font-semibold text-zinc-900 dark:text-zinc-200">最近签到</h3>
        </div>
        <RouterLink to="/records" class="text-xs text-zinc-500 hover:text-emerald-400 transition-colors flex items-center gap-1">
          查看全部
          <ArrowRight class="w-3 h-3" />
        </RouterLink>
      </div>

      <div v-if="recentRecords.length === 0" class="flex flex-col items-center py-8 text-center">
        <Clock class="w-10 h-10 text-zinc-400 dark:text-zinc-700 mb-2" />
        <p class="text-sm text-zinc-500">还没有签到记录</p>
      </div>

      <ol v-else class="relative">
        <div class="absolute left-[7px] top-1.5 bottom-1.5 w-px bg-gradient-to-b from-transparent via-black/[0.06] dark:via-white/[0.06] to-transparent" />
        <li v-for="r in recentRecords" :key="r.id" class="relative pl-7 pb-3 last:pb-0">
          <span
            class="absolute left-0 top-1 w-3.5 h-3.5 rounded-full ring-4 ring-white dark:ring-zinc-900"
            :class="(recordMeta[r.status] || recordMeta.failed).dotBg"
          />
          <div class="flex items-start justify-between gap-3">
            <div class="min-w-0 flex-1">
              <span class="text-sm font-medium" :class="(recordMeta[r.status] || recordMeta.failed).color">
                {{ (recordMeta[r.status] || recordMeta.failed).label }}
              </span>
              <p class="text-xs text-zinc-500 mt-0.5 leading-relaxed break-all">{{ r.message || '—' }}</p>
            </div>
            <span class="shrink-0 text-[10px] text-zinc-500 dark:text-zinc-600 tabular-nums">
              {{ formatDateTime(r.occurredAt) }}
            </span>
          </div>
        </li>
      </ol>
    </section>

    <!-- Token expiry warning -->
    <div
      v-if="me.token.remainingSec < 24 * 3600 && me.token.remainingSec > 0"
      class="rounded-xl bg-amber-500/10 ring-1 ring-amber-500/30 p-4 flex items-start gap-3"
    >
      <AlertTriangle class="w-5 h-5 text-amber-400 shrink-0 mt-0.5" />
      <div class="flex-1">
        <p class="text-sm font-medium text-amber-300">Token 即将过期</p>
        <p class="text-xs text-amber-400/80 mt-1">
          剩余 {{ formatRemaining(me.token.remainingSec) }}，请尽快在「账号」页更新。
        </p>
      </div>
      <RouterLink to="/account" class="shrink-0 text-xs px-3 py-1.5 rounded-md bg-amber-500/20 hover:bg-amber-500/30 text-amber-300 transition-colors">
        前往更新
      </RouterLink>
    </div>
    <div
      v-else-if="me.token.remainingSec <= 0"
      class="rounded-xl bg-red-500/10 ring-1 ring-red-500/30 p-4 flex items-start gap-3"
    >
      <XCircle class="w-5 h-5 text-red-400 shrink-0 mt-0.5" />
      <div class="flex-1">
        <p class="text-sm font-medium text-red-300">Token 已过期</p>
        <p class="text-xs text-red-400/80 mt-1">必须重新完成微信授权并更新 Token，才能继续自动签到。</p>
      </div>
      <RouterLink to="/account" class="shrink-0 text-xs px-3 py-1.5 rounded-md bg-red-500/20 hover:bg-red-500/30 text-red-300 transition-colors">
        前往更新
      </RouterLink>
    </div>
  </div>
</template>
