<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import QRCode from 'qrcode'
import {
  Plus,
  X,
  Trash2,
  Pencil,
  RefreshCw,
  UserPlus,
  Calendar,
  Building2,
  Check,
  AlertCircle,
  QrCode as QrCodeIcon,
  Clock,
  PlayCircle,
  History,
  KeyRound,
  Copy,
} from 'lucide-vue-next'
import type { AdminGuest, Dorm } from '../../types'
import { adminApi, type SchoolOAuthPrepared } from '../../api'
import { formatDateTime } from '../../lib/format'
import { showToast } from '../../lib/toast'
import { copyText } from '../../lib/clipboard'
import { detectSchoolOauthInput } from '../../lib/schoolOauth'
import Avatar from '../../components/Avatar.vue'

const guests = ref<AdminGuest[]>([])
const dorms = ref<Dorm[]>([])
const loading = ref(false)

// Per-card transient state — keyed by userId.
const signing = ref<Record<string, boolean>>({})
const bindingDorm = ref<Record<string, boolean>>({})
const togglingAuto = ref<Record<string, boolean>>({})

const now = ref(new Date())
let tickHandle: number | undefined

// --- create modal state ---
const showCreate = ref(false)
const creating = ref(false)
const cLabel = ref('')
const cDormId = ref<number | 0>(0)
const cDates = ref<string[]>([defaultTodayPlus(1)])
const cCallback = ref('')
const cQrDataUrl = ref('')
const cQrBuilding = ref(false)
const cOAuthAttemptId = ref('')
const cAuthorizationUrl = ref('')

// --- edit modal state ---
const editTarget = ref<AdminGuest | null>(null)
const eLabel = ref('')
const eDates = ref<string[]>([])
const updating = ref(false)

// --- refresh-token modal state ---
const refreshTarget = ref<AdminGuest | null>(null)
const rCallback = ref('')
const rQrDataUrl = ref('')
const rQrBuilding = ref(false)
const rOAuthAttemptId = ref('')
const rAuthorizationUrl = ref('')
const refreshing = ref(false)

function defaultTodayPlus(n: number): string {
  const d = new Date()
  d.setDate(d.getDate() + n)
  return d.toISOString().slice(0, 10)
}

async function load() {
  loading.value = true
  try {
    const [g, d] = await Promise.all([adminApi.listGuests(), adminApi.listDorms()])
    guests.value = g
    dorms.value = d as unknown as Dorm[]
  } catch (e: any) {
    showToast('err', e.message || '加载失败')
  } finally {
    loading.value = false
  }
}
onMounted(() => {
  load()
  tickHandle = window.setInterval(() => (now.value = new Date()), 30_000)
})
onUnmounted(() => {
  if (tickHandle) clearInterval(tickHandle)
})

// --- create flow ---
function openCreate() {
  cLabel.value = ''
  cDormId.value = 0
  cDates.value = [defaultTodayPlus(1)]
  cCallback.value = ''
  showCreate.value = true
  refreshQr()
}

async function refreshQr() {
  cQrBuilding.value = true
  try {
    const prepared = await adminApi.prepareGuestOAuth()
    await applyCreatePrepared(prepared)
  } catch (e: any) {
    cOAuthAttemptId.value = ''
    cAuthorizationUrl.value = ''
    cQrDataUrl.value = ''
    showToast('err', e?.message || '学校登录准备失败')
  } finally {
    cQrBuilding.value = false
  }
}

async function applyCreatePrepared(prepared: SchoolOAuthPrepared) {
  cOAuthAttemptId.value = prepared.attemptId
  cAuthorizationUrl.value = prepared.authorizationUrl
  cQrDataUrl.value = await QRCode.toDataURL(prepared.authorizationUrl, {
    width: 240,
    margin: 1,
    errorCorrectionLevel: 'M',
  })
}

const cCallbackDetect = computed(() => detectSchoolOauthInput(cCallback.value))
const cCallbackOk = computed(() => cCallbackDetect.value.kind === 'callback-url')

function addCreateDate() {
  cDates.value.push(defaultTodayPlus(cDates.value.length + 1))
}
function removeCreateDate(i: number) {
  cDates.value.splice(i, 1)
}

const cCanSubmit = computed(() => {
  if (creating.value) return false
  if (!cLabel.value.trim()) return false
  if (cDates.value.filter(d => d.trim()).length === 0) return false
  if (!cCallbackOk.value) return false
  return true
})

async function submitCreate() {
  if (!cCanSubmit.value) return
  creating.value = true
  try {
    const payload = {
      label: cLabel.value.trim(),
      signDates: cDates.value.filter(d => d.trim()),
      dormId: cDormId.value || undefined,
      callbackUrl: cCallback.value.trim(),
      oauthAttemptId: cOAuthAttemptId.value,
    }
    await adminApi.createGuest(payload)
    showToast('ok', '已添加临时朋友')
    showCreate.value = false
    await load()
  } catch (e: any) {
    showToast('err', e.message || '添加失败')
  } finally {
    creating.value = false
  }
}

// --- edit flow ---
function openEdit(g: AdminGuest) {
  editTarget.value = g
  eLabel.value = g.label
  eDates.value = [...g.signDates]
}

function addEditDate() {
  eDates.value.push(defaultTodayPlus(eDates.value.length + 1))
}
function removeEditDate(i: number) {
  eDates.value.splice(i, 1)
}

async function submitEdit() {
  if (!editTarget.value) return
  updating.value = true
  try {
    await adminApi.updateGuest(editTarget.value.userId, {
      label: eLabel.value.trim(),
      signDates: eDates.value.filter(d => d.trim()),
    })
    showToast('ok', '已更新')
    editTarget.value = null
    await load()
  } catch (e: any) {
    showToast('err', e.message || '保存失败')
  } finally {
    updating.value = false
  }
}

// --- refresh-token flow ---
async function openRefresh(g: AdminGuest) {
  refreshTarget.value = g
  rCallback.value = ''
  await rebuildRefreshQr()
}

async function rebuildRefreshQr() {
  rQrBuilding.value = true
  try {
    if (!refreshTarget.value) return
    const prepared = await adminApi.prepareUserTokenOAuth(refreshTarget.value.userId)
    await applyRefreshPrepared(prepared)
  } catch (e: any) {
    rOAuthAttemptId.value = ''
    rAuthorizationUrl.value = ''
    rQrDataUrl.value = ''
    showToast('err', e?.message || '学校登录准备失败')
  } finally {
    rQrBuilding.value = false
  }
}

async function applyRefreshPrepared(prepared: SchoolOAuthPrepared) {
  rOAuthAttemptId.value = prepared.attemptId
  rAuthorizationUrl.value = prepared.authorizationUrl
  rQrDataUrl.value = await QRCode.toDataURL(prepared.authorizationUrl, {
    width: 240,
    margin: 1,
    errorCorrectionLevel: 'M',
  })
}

const rCallbackDetect = computed(() => detectSchoolOauthInput(rCallback.value))
const rCallbackOk = computed(() => rCallbackDetect.value.kind === 'callback-url')

async function submitRefresh() {
  if (!refreshTarget.value || !rCallbackOk.value) return
  refreshing.value = true
  try {
    await adminApi.refreshUserToken(refreshTarget.value.userId, {
      callbackUrl: rCallback.value.trim(),
      oauthAttemptId: rOAuthAttemptId.value,
    })
    showToast('ok', `Token 已刷新（${refreshTarget.value.label}）`)
    refreshTarget.value = null
    await load()
  } catch (e: any) {
    showToast('err', e.message || '刷新失败')
  } finally {
    refreshing.value = false
  }
}

async function copyCreateAuthorizationUrl() {
  const ok = await copyText(cAuthorizationUrl.value)
  showToast(ok ? 'ok' : 'err', ok ? '授权链接已复制' : '复制失败')
}

async function copyRefreshAuthorizationUrl() {
  const ok = await copyText(rAuthorizationUrl.value)
  showToast(ok ? 'ok' : 'err', ok ? '授权链接已复制' : '复制失败')
}

watch(refreshTarget, v => {
  if (!v) rCallback.value = ''
})

// --- per-card actions ---
async function remove(g: AdminGuest) {
  if (!confirm(`删除临时朋友「${g.label}」(${g.userName})？\n该账号会立即销毁，签到记录一并删除。`)) return
  try {
    await adminApi.deleteGuest(g.userId)
    showToast('ok', '已删除')
    await load()
  } catch (e: any) {
    showToast('err', e.message || '删除失败')
  }
}

async function signNow(g: AdminGuest) {
  if (signing.value[g.userId]) return
  if (!confirm(`代「${g.label}」(${g.userName}) 立即签到？`)) return
  signing.value[g.userId] = true
  try {
    const res = await adminApi.signNowFor(g.userId)
    const tone =
      res.status === 'success' || res.status === 'already' ? 'ok' : 'err'
    showToast(tone, `${g.label}: ${res.message || res.status}`)
    await load()
  } catch (e: any) {
    showToast('err', e.message || '签到失败')
  } finally {
    signing.value[g.userId] = false
  }
}

async function changeDorm(g: AdminGuest, dormId: number) {
  if (bindingDorm.value[g.userId]) return
  bindingDorm.value[g.userId] = true
  try {
    await adminApi.updateUser(g.userId, { dormId })
    showToast('ok', dormId === 0 ? '已解绑宿舍楼' : '已切换宿舍楼')
    await load()
  } catch (e: any) {
    showToast('err', e.message || '保存失败')
  } finally {
    bindingDorm.value[g.userId] = false
  }
}

async function toggleAuto(g: AdminGuest) {
  if (togglingAuto.value[g.userId]) return
  togglingAuto.value[g.userId] = true
  try {
    await adminApi.updateUser(g.userId, { autoSign: !g.autoSign })
    await load()
  } catch (e: any) {
    showToast('err', e.message || '保存失败')
  } finally {
    togglingAuto.value[g.userId] = false
  }
}

// --- display helpers ---
function daysLeft(expiresAt: number | null): string {
  if (!expiresAt) return '永久'
  const cur = Math.floor(now.value.getTime() / 1000)
  if (expiresAt < cur) return '已过期'
  const days = Math.ceil((expiresAt - cur) / 86400)
  return `${days} 天剩余`
}

function daysLeftClass(expiresAt: number | null): string {
  if (!expiresAt) return 'text-zinc-500'
  const cur = Math.floor(now.value.getTime() / 1000)
  if (expiresAt < cur) return 'text-red-400'
  const days = Math.ceil((expiresAt - cur) / 86400)
  if (days <= 1) return 'text-amber-400'
  return 'text-emerald-400'
}

function summarizeDates(dates: string[]): string {
  if (!dates || dates.length === 0) return '—'
  if (dates.length === 1) return dates[0]
  return `${dates[0]} → ${dates[dates.length - 1]} (${dates.length} 天)`
}

function signTimeStr(g: AdminGuest): string {
  return `22:${String(g.triggerMinute).padStart(2, '0')}`
}

// Token urgency for visual highlighting in the card:
//   expired — past tokenExp (immediate action required)
//   soon    — within 48h of expiry (refresh proactively)
//   ok      — fine
function tokenUrgency(g: AdminGuest): 'expired' | 'soon' | 'ok' {
  const cur = Math.floor(now.value.getTime() / 1000)
  if (g.tokenExp <= cur) return 'expired'
  if (g.tokenExp - cur < 48 * 3600) return 'soon'
  return 'ok'
}

// Today's local YYYY-MM-DD.
const todayStr = computed(() => {
  const d = now.value
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
})

// Per-guest sign-state for today:
//   today   — today is one of the sign_dates, scheduled in the future
//   signing — within the 22:0X±60s window
//   skip    — today is not one of the sign_dates
//   expired — past last sign date
function todayState(g: AdminGuest): 'today' | 'signing' | 'skip' | 'expired' {
  if (!g.signDates.includes(todayStr.value)) {
    // Are we past the last date? Then "expired" tone, else "skip" tone.
    if (g.signDates.length > 0 && todayStr.value > g.signDates[g.signDates.length - 1]) {
      return 'expired'
    }
    return 'skip'
  }
  const n = now.value
  const h = n.getHours()
  const m = n.getMinutes()
  // Sign window for guest: 22:triggerMinute, give it ±2 min slack visually.
  if (h === 22 && Math.abs(m - g.triggerMinute) <= 2) return 'signing'
  return 'today'
}

function statusDotClass(status: string): string {
  switch (status) {
    case 'success':
    case 'already':
    case 'exempt':
      return 'bg-emerald-400'
    case 'failed':
      return 'bg-red-400'
    case 'skipped':
      return 'bg-zinc-500'
    default:
      return 'bg-zinc-400'
  }
}

function statusLabel(status: string): string {
  switch (status) {
    case 'success':
      return '成功'
    case 'already':
      return '已签'
    case 'exempt':
      return '免签'
    case 'failed':
      return '失败'
    case 'skipped':
      return '跳过'
    default:
      return status
  }
}

watch(showCreate, v => {
  if (!v) cCallback.value = ''
})
</script>

<template>
  <div class="space-y-4">
    <header class="flex flex-col sm:flex-row sm:items-end sm:justify-between gap-3">
      <div>
        <h1 class="text-2xl font-bold tracking-tight">临时朋友</h1>
        <p class="text-sm text-zinc-500 mt-1">
          admin 代为创建 / 配置 / 手动签到 — 卡片汇总每个临时朋友的所有信息和操作。
        </p>
      </div>
      <button
        @click="openCreate"
        class="self-start inline-flex items-center gap-1.5 bg-emerald-500 hover:bg-emerald-400 text-zinc-950 text-sm font-medium px-4 py-2 rounded-lg transition-colors"
      >
        <Plus class="w-4 h-4" />
        新增临时朋友
      </button>
    </header>

    <!-- Loading / empty -->
    <div
      v-if="loading && guests.length === 0"
      class="py-20 flex items-center justify-center"
    >
      <div class="h-6 w-6 rounded-full border-2 border-zinc-800 border-t-emerald-400 wangui-spin" />
    </div>

    <div
      v-else-if="guests.length === 0"
      class="rounded-xl bg-white/85 dark:bg-zinc-900/60 ring-1 ring-black/[0.08] dark:ring-white/[0.06] py-16 text-center"
    >
      <UserPlus class="w-8 h-8 mx-auto text-zinc-400 dark:text-zinc-600 mb-3" />
      <p class="text-sm text-zinc-500">还没有临时朋友</p>
      <p class="text-xs text-zinc-400 dark:text-zinc-600 mt-1">点右上角「新增」开始</p>
    </div>

    <!-- Card grid -->
    <section
      v-else
      class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-3"
    >
      <article
        v-for="g in guests"
        :key="g.userId"
        class="flex flex-col rounded-2xl bg-white/85 dark:bg-zinc-900/60 ring-1 ring-black/[0.08] dark:ring-white/[0.06] overflow-hidden"
      >
        <!-- Header: avatar + identity + delete -->
        <header class="p-4 flex items-start gap-3 border-b border-black/[0.05] dark:border-white/[0.04]">
          <Avatar
            :src="g.userAvatarUrl"
            :name="g.userName"
            :size="44"
            rounded="lg"
          />
          <div class="min-w-0 flex-1">
            <div class="flex items-center gap-1.5">
              <h3 class="text-base font-semibold text-zinc-900 dark:text-zinc-100 truncate">
                {{ g.userName }}
              </h3>
              <span
                class="shrink-0 inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium bg-amber-500/15 text-amber-700 dark:text-amber-300 ring-1 ring-amber-500/30"
              >
                临时
              </span>
            </div>
            <p class="text-[11px] text-zinc-500 font-mono-token truncate mt-0.5">
              {{ g.userNumber }}
            </p>
            <p
              v-if="g.userSection || g.userClass"
              class="text-[11px] text-zinc-500 truncate"
            >
              {{ g.userSection }}{{ g.userSection && g.userClass ? ' · ' : '' }}{{ g.userClass }}
            </p>
          </div>
          <button
            @click="remove(g)"
            title="删除"
            class="p-1.5 rounded text-zinc-500 dark:text-zinc-400 hover:bg-red-500/10 hover:text-red-400 transition-colors shrink-0"
          >
            <Trash2 class="w-4 h-4" />
          </button>
        </header>

        <!-- Label -->
        <div class="px-4 pt-3 text-xs">
          <span class="text-zinc-500">备注名：</span>
          <span class="text-zinc-900 dark:text-zinc-200 font-medium">{{ g.label || '—' }}</span>
        </div>

        <!-- Body fields -->
        <div class="p-4 space-y-2.5 text-xs flex-1">
          <!-- Dorm picker -->
          <div class="flex items-center gap-2">
            <Building2 class="w-3.5 h-3.5 text-zinc-500 shrink-0" />
            <span class="text-zinc-500 shrink-0 w-14">宿舍楼</span>
            <select
              :value="g.dormId ?? 0"
              :disabled="bindingDorm[g.userId]"
              @change="(e) => changeDorm(g, +((e.target as HTMLSelectElement).value))"
              class="flex-1 bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-md px-2 py-1 text-xs focus-ring text-zinc-900 dark:text-zinc-200 disabled:opacity-50"
            >
              <option :value="0">— 未绑定 —</option>
              <option v-for="d in dorms" :key="d.id" :value="d.id">{{ d.name }}</option>
            </select>
          </div>

          <!-- AutoSign toggle -->
          <div class="flex items-center gap-2">
            <PlayCircle class="w-3.5 h-3.5 text-zinc-500 shrink-0" />
            <span class="text-zinc-500 shrink-0 w-14">自动签到</span>
            <button
              type="button"
              :disabled="togglingAuto[g.userId]"
              @click="toggleAuto(g)"
              :class="g.autoSign ? 'bg-emerald-500' : 'bg-zinc-300 dark:bg-zinc-700'"
              class="relative inline-flex h-5 w-9 items-center rounded-full transition-colors disabled:opacity-50"
            >
              <span
                :class="g.autoSign ? 'translate-x-4' : 'translate-x-0.5'"
                class="inline-block h-4 w-4 transform rounded-full bg-white shadow transition-transform"
              />
            </button>
            <span class="text-[11px]" :class="g.autoSign ? 'text-emerald-500 dark:text-emerald-300' : 'text-zinc-500'">
              {{ g.autoSign ? '已开启' : '已关闭' }}
            </span>
          </div>

          <!-- Sign time -->
          <div class="flex items-center gap-2">
            <Clock class="w-3.5 h-3.5 text-zinc-500 shrink-0" />
            <span class="text-zinc-500 shrink-0 w-14">签到时刻</span>
            <template v-if="todayState(g) === 'signing'">
              <span class="text-amber-400 font-semibold">{{ signTimeStr(g) }}</span>
              <span class="text-[10px] text-amber-400/70">执行中</span>
            </template>
            <template v-else-if="todayState(g) === 'today'">
              <span class="text-emerald-400 font-semibold tabular-nums">{{ signTimeStr(g) }}</span>
              <span class="text-[10px] text-zinc-500">±{{ g.jitterSec }}s · 今天</span>
            </template>
            <template v-else-if="todayState(g) === 'expired'">
              <span class="text-zinc-500">已过期</span>
            </template>
            <template v-else>
              <span class="text-zinc-500 tabular-nums">{{ signTimeStr(g) }}</span>
              <span class="text-[10px] text-zinc-500">今天不签</span>
            </template>
          </div>

          <!-- Dates -->
          <div class="flex items-start gap-2">
            <Calendar class="w-3.5 h-3.5 text-zinc-500 shrink-0 mt-0.5" />
            <span class="text-zinc-500 shrink-0 w-14">签到日期</span>
            <div class="min-w-0 flex-1">
              <p class="text-zinc-900 dark:text-zinc-200 font-mono-token truncate">
                {{ summarizeDates(g.signDates) }}
              </p>
              <p class="text-[10px] mt-0.5 font-medium" :class="daysLeftClass(g.expiresAt)">
                {{ daysLeft(g.expiresAt) }}
              </p>
            </div>
          </div>

          <!-- Token -->
          <div class="flex items-center gap-2">
            <KeyRound class="w-3.5 h-3.5 text-zinc-500 shrink-0" />
            <span class="text-zinc-500 shrink-0 w-14">Token</span>
            <span
              class="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] font-medium shrink-0"
              :class="tokenUrgency(g) === 'expired'
                ? 'bg-red-500/10 text-red-700 dark:text-red-300 ring-1 ring-red-500/25'
                : tokenUrgency(g) === 'soon'
                  ? 'bg-amber-500/10 text-amber-700 dark:text-amber-300 ring-1 ring-amber-500/30'
                  : 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-300 ring-1 ring-emerald-500/25'"
            >
              <span
                class="w-1 h-1 rounded-full"
                :class="tokenUrgency(g) === 'expired' ? 'bg-red-400' : tokenUrgency(g) === 'soon' ? 'bg-amber-400' : 'bg-emerald-400'"
              />
              {{ tokenUrgency(g) === 'expired' ? '已失效' : tokenUrgency(g) === 'soon' ? '快过期' : '有效' }}
            </span>
            <span class="text-[10px] text-zinc-500 truncate flex-1" :title="formatDateTime(g.tokenExp)">
              {{ formatDateTime(g.tokenExp) }}
            </span>
            <button
              @click="openRefresh(g)"
              :class="tokenUrgency(g) === 'expired'
                ? 'bg-red-500 hover:bg-red-400 text-white'
                : tokenUrgency(g) === 'soon'
                  ? 'bg-amber-500 hover:bg-amber-400 text-zinc-950'
                  : 'bg-white/80 dark:bg-zinc-900/80 text-zinc-700 dark:text-zinc-300 ring-1 ring-black/[0.08] dark:ring-white/[0.06] hover:ring-emerald-500/40'"
              class="inline-flex items-center gap-1 px-2 py-0.5 rounded text-[10px] font-medium transition-colors shrink-0"
              :title="tokenUrgency(g) === 'expired' ? '立即刷新（已失效）' : '让本人重新授权以刷新 Token'"
            >
              <RefreshCw class="w-3 h-3" />
              刷新
            </button>
          </div>
        </div>

        <!-- Recent records -->
        <div
          v-if="g.recentRecords && g.recentRecords.length > 0"
          class="px-4 pb-3"
        >
          <div class="flex items-center gap-1.5 mb-1.5">
            <History class="w-3 h-3 text-zinc-500" />
            <span class="text-[10px] text-zinc-500 uppercase tracking-wide">最近记录</span>
          </div>
          <ul class="space-y-1">
            <li
              v-for="r in g.recentRecords.slice(0, 3)"
              :key="r.id"
              class="flex items-center gap-2 text-[11px]"
            >
              <span class="w-1.5 h-1.5 rounded-full shrink-0" :class="statusDotClass(r.status)" />
              <span class="shrink-0 text-zinc-700 dark:text-zinc-300 font-medium w-7">
                {{ statusLabel(r.status) }}
              </span>
              <span class="text-zinc-500 font-mono-token text-[10px] shrink-0">
                {{ formatDateTime(r.occurredAt) }}
              </span>
              <span
                v-if="r.message"
                class="text-zinc-500 truncate"
                :title="r.message"
              >
                · {{ r.message }}
              </span>
            </li>
          </ul>
        </div>

        <!-- Actions -->
        <footer class="px-4 py-3 border-t border-black/[0.05] dark:border-white/[0.04] flex gap-2">
          <button
            @click="signNow(g)"
            :disabled="signing[g.userId]"
            class="flex-1 inline-flex items-center justify-center gap-1.5 bg-emerald-500 hover:bg-emerald-400 disabled:opacity-50 text-zinc-950 text-xs font-medium px-3 py-1.5 rounded-md transition-colors"
            title="代签到（应急 / 测试）"
          >
            <PlayCircle class="w-3.5 h-3.5" />
            {{ signing[g.userId] ? '签到中…' : '立即签到' }}
          </button>
          <button
            @click="openEdit(g)"
            class="inline-flex items-center justify-center gap-1 px-3 py-1.5 rounded-md text-xs text-zinc-700 dark:text-zinc-300 bg-white/80 dark:bg-zinc-900/80 ring-1 ring-black/[0.08] dark:ring-white/[0.06] hover:ring-emerald-500/40 transition-colors"
            title="编辑日期 / 续期"
          >
            <Pencil class="w-3.5 h-3.5" />
            日期
          </button>
        </footer>
      </article>
    </section>

    <!-- Create modal -->
    <Transition name="modal">
      <div v-if="showCreate" class="fixed inset-0 z-50 bg-white/80 dark:bg-zinc-950/80 backdrop-blur flex items-center justify-center p-4 overflow-y-auto"
        @click.self="showCreate = false">
        <div class="w-full max-w-2xl bg-zinc-100 dark:bg-zinc-900 ring-1 ring-black/10 dark:ring-white/10 rounded-2xl shadow-2xl my-8">
          <div class="p-5 border-b border-black/[0.08] dark:border-white/[0.06] flex items-center justify-between">
            <h2 class="text-base font-bold flex items-center gap-2">
              <UserPlus class="w-4 h-4 text-emerald-400" />
              新增临时朋友
            </h2>
            <button @click="showCreate = false"
              class="text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-200 transition-colors">
              <X class="w-4 h-4" />
            </button>
          </div>

          <div class="p-5 space-y-4">
            <!-- Label + Dorm -->
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <div>
                <label class="block text-xs text-zinc-500 mb-1.5">备注名 *</label>
                <input v-model="cLabel" placeholder="如「张三 5/20」"
                  class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-lg px-3 py-2 text-sm focus-ring text-zinc-900 dark:text-zinc-200 placeholder:text-zinc-400 dark:placeholder:text-zinc-600" />
              </div>
              <div>
                <label class="block text-xs text-zinc-500 mb-1.5 inline-flex items-center gap-1">
                  <Building2 class="w-3 h-3" />
                  宿舍楼
                </label>
                <select v-model="cDormId"
                  class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-lg px-3 py-2 text-sm focus-ring text-zinc-900 dark:text-zinc-200">
                  <option :value="0">不绑定</option>
                  <option v-for="d in dorms" :key="d.id" :value="d.id">{{ d.name }}</option>
                </select>
              </div>
            </div>

            <!-- Dates -->
            <div>
              <label class="text-xs text-zinc-500 inline-flex items-center gap-1 mb-2">
                <Calendar class="w-3 h-3" />
                签到日期 *（选哪几天 22:00 帮他签）
              </label>
              <div class="space-y-1.5">
                <div v-for="(_, i) in cDates" :key="i" class="flex items-center gap-2">
                  <input type="date" v-model="cDates[i]"
                    class="flex-1 bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-lg px-3 py-1.5 text-sm font-mono-token focus-ring text-zinc-900 dark:text-zinc-200" />
                  <button type="button" @click="removeCreateDate(i)" :disabled="cDates.length === 1"
                    class="p-1.5 rounded hover:bg-black/5 dark:hover:bg-white/5 text-zinc-500 hover:text-red-400 disabled:opacity-30 disabled:hover:text-zinc-500 transition-colors">
                    <Trash2 class="w-3.5 h-3.5" />
                  </button>
                </div>
                <button type="button" @click="addCreateDate"
                  class="text-[11px] text-emerald-400 hover:text-emerald-300 inline-flex items-center gap-1">
                  <Plus class="w-3 h-3" />
                  再加一天
                </button>
              </div>
            </div>

            <!-- QR -->
            <div class="rounded-xl bg-white/70 dark:bg-zinc-950/70 ring-1 ring-black/[0.05] dark:ring-white/[0.04] p-3">
              <div class="flex items-center gap-2 mb-2">
                <QrCodeIcon class="w-3.5 h-3.5 text-zinc-500" />
                <span class="text-xs text-zinc-500">电脑微信授权（本轮约 5 分钟有效）</span>
              </div>
              <div class="flex flex-col sm:flex-row gap-3">
                <div class="shrink-0 self-center sm:self-start">
                  <div class="w-36 h-36 rounded-xl bg-white ring-1 ring-black/[0.06] p-2 flex items-center justify-center overflow-hidden">
                    <img v-if="cQrDataUrl" :src="cQrDataUrl" alt="二维码" class="w-full h-full object-contain" />
                    <div v-else class="text-[11px] text-zinc-400 text-center">{{ cQrBuilding ? '生成中…' : '失败' }}</div>
                  </div>
                </div>
                <div class="min-w-0 flex-1">
                  <ol class="text-[11px] text-zinc-600 dark:text-zinc-400 space-y-1 list-decimal list-inside leading-relaxed">
                    <li>复制本轮授权链接发给朋友</li>
                    <li>朋友在<strong>电脑微信</strong>打开并授权</li>
                    <li>看到学校图片页后复制完整链接</li>
                    <li>朋友把链接发回，你粘到下面 ↓</li>
                  </ol>
                  <button type="button" @click="copyCreateAuthorizationUrl" :disabled="!cAuthorizationUrl"
                    class="mt-2 mr-2 inline-flex items-center gap-1 px-2.5 py-1 rounded-md text-[11px] text-zinc-700 dark:text-zinc-300 bg-white/80 dark:bg-zinc-900/80 ring-1 ring-black/[0.06] dark:ring-white/[0.05] hover:ring-emerald-500/40 transition-colors disabled:opacity-50">
                    <Copy class="w-3 h-3" />
                    复制授权链接
                  </button>
                  <button type="button" @click="refreshQr"
                    class="mt-2 inline-flex items-center gap-1 px-2.5 py-1 rounded-md text-[11px] text-zinc-700 dark:text-zinc-300 bg-white/80 dark:bg-zinc-900/80 ring-1 ring-black/[0.06] dark:ring-white/[0.05] hover:ring-emerald-500/40 transition-colors">
                    <RefreshCw class="w-3 h-3" />
                    重新生成
                  </button>
                </div>
              </div>
            </div>

            <!-- Callback paste -->
            <div>
              <label class="block text-xs text-zinc-500 mb-1.5">把朋友给的回调链接粘到这里 *</label>
              <textarea v-model="cCallback"
                placeholder="https://xhbcs.henau.edu.cn/农大白.png?code=EXAMPLE&state=EXAMPLE"
                class="w-full bg-white dark:bg-zinc-950 ring-1 ring-emerald-500/30 focus:!ring-emerald-500/60 rounded-lg px-3 py-2 h-20 resize-none text-sm font-mono-token text-zinc-900 dark:text-zinc-200 placeholder:text-zinc-400 dark:placeholder:text-zinc-600 focus-ring" />
              <div v-if="cCallbackDetect.kind === 'callback-url'"
                class="mt-1.5 inline-flex items-center gap-1.5 px-2 py-1 rounded text-[11px] text-emerald-400 bg-emerald-500/10">
                <Check class="w-3 h-3" />
                已识别回调链接
              </div>
              <div v-else-if="cCallbackDetect.kind === 'invalid' && cCallback.trim()"
                class="mt-1.5 inline-flex items-center gap-1.5 px-2 py-1 rounded text-[11px] text-amber-400 bg-amber-500/10">
                <AlertCircle class="w-3 h-3" />
                请粘贴本轮学校图片页的完整回调 URL
              </div>
            </div>
          </div>

          <div class="px-5 pb-5 flex justify-end gap-2">
            <button @click="showCreate = false"
              class="px-4 py-2 text-sm text-zinc-500 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-200 transition-colors">
              取消
            </button>
            <button @click="submitCreate" :disabled="!cCanSubmit"
              class="bg-emerald-500 hover:bg-emerald-400 disabled:opacity-50 text-zinc-950 text-sm font-medium px-5 py-2 rounded-lg transition-colors">
              {{ creating ? '创建中…' : '创建临时朋友' }}
            </button>
          </div>
        </div>
      </div>
    </Transition>

    <!-- Edit modal -->
    <Transition name="modal">
      <div v-if="editTarget" class="fixed inset-0 z-50 bg-white/80 dark:bg-zinc-950/80 backdrop-blur flex items-center justify-center p-4"
        @click.self="editTarget = null">
        <div class="w-full max-w-md bg-zinc-100 dark:bg-zinc-900 ring-1 ring-black/10 dark:ring-white/10 rounded-2xl shadow-2xl">
          <div class="p-5 border-b border-black/[0.08] dark:border-white/[0.06] flex items-center justify-between">
            <h2 class="text-base font-bold">编辑临时朋友</h2>
            <button @click="editTarget = null"
              class="text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-200 transition-colors">
              <X class="w-4 h-4" />
            </button>
          </div>
          <div class="p-5 space-y-4">
            <div>
              <p class="text-[11px] text-zinc-500">姓名 / 学号</p>
              <p class="text-sm">
                <span>{{ editTarget.userName }}</span>
                <span class="ml-1.5 text-zinc-500 font-mono-token">/ {{ editTarget.userNumber }}</span>
              </p>
            </div>
            <div>
              <label class="block text-xs text-zinc-500 mb-1.5">备注名</label>
              <input v-model="eLabel"
                class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-lg px-3 py-2 text-sm focus-ring text-zinc-900 dark:text-zinc-200" />
            </div>
            <div>
              <label class="text-xs text-zinc-500 inline-flex items-center gap-1 mb-2">
                <Calendar class="w-3 h-3" />
                签到日期
              </label>
              <div class="space-y-1.5">
                <div v-for="(_, i) in eDates" :key="i" class="flex items-center gap-2">
                  <input type="date" v-model="eDates[i]"
                    class="flex-1 bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-lg px-3 py-1.5 text-sm font-mono-token focus-ring text-zinc-900 dark:text-zinc-200" />
                  <button type="button" @click="removeEditDate(i)" :disabled="eDates.length === 1"
                    class="p-1.5 rounded hover:bg-black/5 dark:hover:bg-white/5 text-zinc-500 hover:text-red-400 disabled:opacity-30 transition-colors">
                    <Trash2 class="w-3.5 h-3.5" />
                  </button>
                </div>
                <button type="button" @click="addEditDate"
                  class="text-[11px] text-emerald-400 hover:text-emerald-300 inline-flex items-center gap-1">
                  <Plus class="w-3 h-3" />
                  再加一天（续期）
                </button>
              </div>
            </div>
          </div>
          <div class="px-5 pb-5 flex justify-end gap-2">
            <button @click="editTarget = null"
              class="px-4 py-2 text-sm text-zinc-500 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-200 transition-colors">
              取消
            </button>
            <button @click="submitEdit" :disabled="updating"
              class="bg-emerald-500 hover:bg-emerald-400 disabled:opacity-50 text-zinc-950 text-sm font-medium px-5 py-2 rounded-lg transition-colors">
              {{ updating ? '保存中…' : '保存' }}
            </button>
          </div>
        </div>
      </div>
    </Transition>

    <!-- Refresh-token modal -->
    <Transition name="modal">
      <div v-if="refreshTarget" class="fixed inset-0 z-50 bg-white/80 dark:bg-zinc-950/80 backdrop-blur flex items-center justify-center p-4 overflow-y-auto"
        @click.self="refreshTarget = null">
        <div class="w-full max-w-2xl bg-zinc-100 dark:bg-zinc-900 ring-1 ring-black/10 dark:ring-white/10 rounded-2xl shadow-2xl my-8">
          <div class="p-5 border-b border-black/[0.08] dark:border-white/[0.06] flex items-center justify-between">
            <h2 class="text-base font-bold flex items-center gap-2">
              <KeyRound class="w-4 h-4 text-amber-400" />
              刷新 Token — {{ refreshTarget.label }}
            </h2>
            <button @click="refreshTarget = null"
              class="text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-200 transition-colors">
              <X class="w-4 h-4" />
            </button>
          </div>

          <div class="p-5 space-y-4">
            <div class="rounded-lg bg-amber-500/10 ring-1 ring-amber-500/30 px-3 py-2 text-xs text-amber-700 dark:text-amber-200">
              学校 JWT 通常 ~7 天过期。让本人在<strong>电脑微信</strong>打开本轮授权链接，把学校图片页的完整回调 URL 发回。
              新 Token 的学号必须是 <span class="font-mono-token">{{ refreshTarget.userNumber }}</span>，否则会被拒绝。
            </div>

            <!-- Existing identity (read-only) -->
            <div class="text-xs text-zinc-500 flex items-center gap-3">
              <Avatar :src="refreshTarget.userAvatarUrl" :name="refreshTarget.userName" :size="32" rounded="lg" />
              <div class="min-w-0 flex-1">
                <p class="text-sm text-zinc-900 dark:text-zinc-200 truncate">{{ refreshTarget.userName }}</p>
                <p class="text-[11px] font-mono-token truncate">{{ refreshTarget.userNumber }}</p>
              </div>
              <div class="text-right shrink-0">
                <p class="text-[10px] uppercase tracking-wide">当前到期</p>
                <p class="text-[11px] font-mono-token">{{ formatDateTime(refreshTarget.tokenExp) }}</p>
              </div>
            </div>

            <!-- QR -->
            <div class="rounded-xl bg-white/70 dark:bg-zinc-950/70 ring-1 ring-black/[0.05] dark:ring-white/[0.04] p-3">
              <div class="flex items-center gap-2 mb-2">
                <QrCodeIcon class="w-3.5 h-3.5 text-zinc-500" />
                <span class="text-xs text-zinc-500">本轮授权（约 5 分钟有效）</span>
              </div>
              <div class="flex flex-col sm:flex-row gap-3">
                <div class="shrink-0 self-center sm:self-start">
                  <div class="w-36 h-36 rounded-xl bg-white ring-1 ring-black/[0.06] p-2 flex items-center justify-center overflow-hidden">
                    <img v-if="rQrDataUrl" :src="rQrDataUrl" alt="二维码" class="w-full h-full object-contain" />
                    <div v-else class="text-[11px] text-zinc-400 text-center">{{ rQrBuilding ? '生成中…' : '失败' }}</div>
                  </div>
                </div>
                <div class="min-w-0 flex-1">
                  <ol class="text-[11px] text-zinc-600 dark:text-zinc-400 space-y-1 list-decimal list-inside leading-relaxed">
                    <li>复制链接发给本人（<strong>不要换人</strong>）</li>
                    <li>本人在电脑微信打开并授权</li>
                    <li>看到学校图片页后复制完整链接发回</li>
                    <li>粘到下面；纯 code 不可用</li>
                  </ol>
                  <button type="button" @click="copyRefreshAuthorizationUrl" :disabled="!rAuthorizationUrl"
                    class="mt-2 mr-2 inline-flex items-center gap-1 px-2.5 py-1 rounded-md text-[11px] text-zinc-700 dark:text-zinc-300 bg-white/80 dark:bg-zinc-900/80 ring-1 ring-black/[0.06] dark:ring-white/[0.05] hover:ring-emerald-500/40 transition-colors disabled:opacity-50">
                    <Copy class="w-3 h-3" />
                    复制授权链接
                  </button>
                  <button type="button" @click="rebuildRefreshQr"
                    class="mt-2 inline-flex items-center gap-1 px-2.5 py-1 rounded-md text-[11px] text-zinc-700 dark:text-zinc-300 bg-white/80 dark:bg-zinc-900/80 ring-1 ring-black/[0.06] dark:ring-white/[0.05] hover:ring-emerald-500/40 transition-colors">
                    <RefreshCw class="w-3 h-3" />
                    重新生成
                  </button>
                </div>
              </div>
            </div>

            <!-- Callback paste -->
            <div>
              <label class="block text-xs text-zinc-500 mb-1.5">把朋友给的回调链接粘到这里 *</label>
              <textarea v-model="rCallback"
                placeholder="https://xhbcs.henau.edu.cn/农大白.png?code=EXAMPLE&state=EXAMPLE"
                class="w-full bg-white dark:bg-zinc-950 ring-1 ring-emerald-500/30 focus:!ring-emerald-500/60 rounded-lg px-3 py-2 h-20 resize-none text-sm font-mono-token text-zinc-900 dark:text-zinc-200 placeholder:text-zinc-400 dark:placeholder:text-zinc-600 focus-ring" />
              <div v-if="rCallbackDetect.kind === 'callback-url'"
                class="mt-1.5 inline-flex items-center gap-1.5 px-2 py-1 rounded text-[11px] text-emerald-400 bg-emerald-500/10">
                <Check class="w-3 h-3" />
                已识别回调链接
              </div>
              <div v-else-if="rCallbackDetect.kind === 'invalid' && rCallback.trim()"
                class="mt-1.5 inline-flex items-center gap-1.5 px-2 py-1 rounded text-[11px] text-amber-400 bg-amber-500/10">
                <AlertCircle class="w-3 h-3" />
                请粘贴本轮学校图片页的完整回调 URL
              </div>
            </div>
          </div>

          <div class="px-5 pb-5 flex justify-end gap-2">
            <button @click="refreshTarget = null"
              class="px-4 py-2 text-sm text-zinc-500 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-200 transition-colors">
              取消
            </button>
            <button @click="submitRefresh" :disabled="!rCallbackOk || refreshing"
              class="bg-emerald-500 hover:bg-emerald-400 disabled:opacity-50 text-zinc-950 text-sm font-medium px-5 py-2 rounded-lg transition-colors">
              {{ refreshing ? '刷新中…' : '刷新 Token' }}
            </button>
          </div>
        </div>
      </div>
    </Transition>
  </div>
</template>

<style scoped>
.modal-enter-active, .modal-leave-active { transition: opacity 0.2s; }
.modal-enter-from, .modal-leave-to { opacity: 0; }
</style>
