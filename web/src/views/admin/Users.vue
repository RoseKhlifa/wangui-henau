<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import QRCode from 'qrcode'
import {
  Search,
  RefreshCw,
  X,
  KeyRound,
  Copy,
  AlertCircle,
  Check,
  QrCode as QrCodeIcon,
  LayoutGrid,
  List as ListIcon,
  CheckCircle2,
  XCircle,
  Power,
  Trash2,
  PlayCircle,
  CheckSquare,
  Square,
} from 'lucide-vue-next'
import type { AdminUser, Dorm, SchoolCheckinStatus } from '../../types'
import { adminApi, type SchoolOAuthPrepared } from '../../api'
import { formatDateTime, formatRemaining } from '../../lib/format'
import { showToast } from '../../lib/toast'
import { copyText } from '../../lib/clipboard'
import { detectSchoolOauthInput } from '../../lib/schoolOauth'
import Avatar from '../../components/Avatar.vue'
import UserCard from '../../components/admin/UserCard.vue'

const users = ref<AdminUser[]>([])
const dorms = ref<Dorm[]>([])
const loading = ref(false)
const search = ref('')

const now = ref(new Date())
let tickHandle: number | undefined

// Per-card transient flags, keyed by userId.
const signing = ref<Record<string, boolean>>({})
const bindingDorm = ref<Record<string, boolean>>({})
const togglingAuto = ref<Record<string, boolean>>({})
const savingDays = ref<Record<string, boolean>>({})
const togglingDisabled = ref<Record<string, boolean>>({})

// School "today status" per user. 'loading' marker = fetch in flight.
type StatusEntry = SchoolCheckinStatus | 'loading' | undefined
const statusByUser = ref<Record<string, StatusEntry>>({})

// View-mode toggle. Persisted to localStorage so refreshing keeps the choice.
type ViewMode = 'card' | 'list'
const viewMode = ref<ViewMode>('card')
function initViewMode() {
  try {
    const v = localStorage.getItem('admin-users-view')
    if (v === 'card' || v === 'list') viewMode.value = v
  } catch {
    /* localStorage unavailable, keep default */
  }
}
function setViewMode(m: ViewMode) {
  viewMode.value = m
  try {
    localStorage.setItem('admin-users-view', m)
  } catch {
    /* ignore */
  }
}

// Multi-select for bulk operations in list view. Set of userIds.
const selectedIds = ref<Set<string>>(new Set())
const bulkBusy = ref(false)

function isSelected(uid: string): boolean {
  return selectedIds.value.has(uid)
}
function toggleSelect(uid: string) {
  const next = new Set(selectedIds.value)
  if (next.has(uid)) next.delete(uid)
  else next.add(uid)
  selectedIds.value = next
}
const allSelected = computed(
  () => users.value.length > 0 && selectedIds.value.size === users.value.length,
)
const someSelected = computed(
  () => selectedIds.value.size > 0 && selectedIds.value.size < users.value.length,
)
function toggleSelectAll() {
  if (allSelected.value) {
    selectedIds.value = new Set()
  } else {
    selectedIds.value = new Set(users.value.map(u => u.userId))
  }
}
function clearSelection() {
  selectedIds.value = new Set()
}
// Drop any selected ids that no longer exist after a reload (e.g. after bulk delete).
function pruneSelection() {
  const valid = new Set(users.value.map(u => u.userId))
  const next = new Set<string>()
  for (const id of selectedIds.value) if (valid.has(id)) next.add(id)
  selectedIds.value = next
}

async function bulkAction(action: 'enable' | 'disable' | 'sign' | 'delete') {
  const ids = Array.from(selectedIds.value)
  if (ids.length === 0 || bulkBusy.value) return
  const targets = users.value.filter(u => ids.includes(u.userId))
  const label = {
    enable: '启用',
    disable: '禁用',
    sign: '立即签到',
    delete: '删除',
  }[action]
  const warn = action === 'delete'
    ? `删除 ${ids.length} 个用户？将同时释放他们的邀请码。\n这个操作不可撤销。`
    : `${label} ${ids.length} 个用户？`
  if (!confirm(warn)) return
  bulkBusy.value = true
  let ok = 0
  let fail = 0
  try {
    const results = await Promise.allSettled(
      targets.map(u => {
        switch (action) {
          case 'enable':
            return adminApi.updateUser(u.userId, { isDisabled: false })
          case 'disable':
            return adminApi.updateUser(u.userId, { isDisabled: true })
          case 'sign':
            return adminApi.signNowFor(u.userId)
          case 'delete':
            return adminApi.deleteUser(u.userId)
        }
      }),
    )
    for (const r of results) {
      if (r.status === 'fulfilled') ok++
      else fail++
    }
    if (fail === 0) {
      showToast('ok', `${label}已完成 (${ok})`)
    } else if (ok === 0) {
      showToast('err', `${label}全部失败 (${fail})`)
    } else {
      showToast('err', `${label}部分失败：成功 ${ok} / 失败 ${fail}`)
    }
    await load()
    pruneSelection()
  } finally {
    bulkBusy.value = false
  }
}

// In list view, clicking a row opens this drawer with the full UserCard
// inside so admin can edit everything without leaving the page.
const drawerUserId = ref<string | null>(null)
const drawerUser = computed(() => {
  if (!drawerUserId.value) return null
  return users.value.find(u => u.userId === drawerUserId.value) ?? null
})
function openDrawer(u: AdminUser) {
  drawerUserId.value = u.userId
}
function closeDrawer() {
  drawerUserId.value = null
}

// PIN reset modal state
const resetting = ref(false)
const newPinResult = ref<{ user: string; pin: string } | null>(null)

// Refresh-token modal state
const refreshTarget = ref<AdminUser | null>(null)
const rCallback = ref('')
const rQrDataUrl = ref('')
const rQrBuilding = ref(false)
const rOAuthAttemptId = ref('')
const rAuthorizationUrl = ref('')
const refreshing = ref(false)

async function load() {
  loading.value = true
  try {
    const [u, d] = await Promise.all([
      adminApi.listUsers(search.value.trim()),
      adminApi.listDorms(),
    ])
    users.value = u
    dorms.value = d as unknown as Dorm[]
    fetchAllStatus()
  } catch (e: any) {
    showToast('err', e.message || '加载失败')
  } finally {
    loading.value = false
  }
}

async function fetchAllStatus() {
  const next: Record<string, StatusEntry> = {}
  for (const u of users.value) next[u.userId] = 'loading'
  statusByUser.value = next
  await Promise.allSettled(
    users.value.map(async u => {
      try {
        const s = await adminApi.checkinStatusFor(u.userId)
        statusByUser.value[u.userId] = s
      } catch (e: any) {
        statusByUser.value[u.userId] = {
          state: 'error',
          message: e?.message || '查询失败',
        } as SchoolCheckinStatus
      }
    }),
  )
}

async function refreshStatus(u: AdminUser) {
  statusByUser.value[u.userId] = 'loading'
  try {
    statusByUser.value[u.userId] = await adminApi.checkinStatusFor(u.userId)
  } catch (e: any) {
    statusByUser.value[u.userId] = {
      state: 'error',
      message: e?.message || '查询失败',
    } as SchoolCheckinStatus
  }
}

onMounted(() => {
  initViewMode()
  load()
  tickHandle = window.setInterval(() => (now.value = new Date()), 30_000)
})
onUnmounted(() => {
  if (tickHandle) clearInterval(tickHandle)
})

// --- per-card actions ---

async function signNow(u: AdminUser) {
  if (signing.value[u.userId]) return
  if (!confirm(`代「${u.userName}」(${u.userNumber}) 立即签到？`)) return
  signing.value[u.userId] = true
  try {
    const res = await adminApi.signNowFor(u.userId)
    const tone =
      res.status === 'success' || res.status === 'already' ? 'ok' : 'err'
    showToast(tone, `${u.userName}: ${res.message || res.status}`)
    await load()
  } catch (e: any) {
    showToast('err', e.message || '签到失败')
  } finally {
    signing.value[u.userId] = false
  }
}

async function changeDorm(u: AdminUser, dormId: number) {
  if (bindingDorm.value[u.userId]) return
  bindingDorm.value[u.userId] = true
  try {
    const updated = await adminApi.updateUser(u.userId, { dormId })
    Object.assign(u, updated)
    await load()
    showToast('ok', dormId === 0 ? '已解绑宿舍楼' : '已切换宿舍楼')
  } catch (e: any) {
    showToast('err', e.message || '保存失败')
  } finally {
    bindingDorm.value[u.userId] = false
  }
}

async function toggleAuto(u: AdminUser) {
  if (togglingAuto.value[u.userId]) return
  togglingAuto.value[u.userId] = true
  try {
    const updated = await adminApi.updateUser(u.userId, { autoSign: !u.autoSign })
    Object.assign(u, updated)
  } catch (e: any) {
    showToast('err', e.message || '保存失败')
  } finally {
    togglingAuto.value[u.userId] = false
  }
}

async function toggleDay(u: AdminUser, bit: number) {
  if (savingDays.value[u.userId]) return
  savingDays.value[u.userId] = true
  const original = u.signDays
  const next = u.signDays ^ (1 << bit)
  u.signDays = next // optimistic
  try {
    await adminApi.updateUser(u.userId, { signDays: next })
  } catch (e: any) {
    u.signDays = original
    showToast('err', e.message || '保存失败')
  } finally {
    savingDays.value[u.userId] = false
  }
}

async function toggleDisabled(u: AdminUser) {
  if (togglingDisabled.value[u.userId]) return
  if (!confirm(`${u.isDisabled ? '启用' : '禁用'} 「${u.userName}」(${u.userNumber})？`)) return
  togglingDisabled.value[u.userId] = true
  try {
    const updated = await adminApi.updateUser(u.userId, { isDisabled: !u.isDisabled })
    Object.assign(u, updated)
    showToast('ok', u.isDisabled ? '已禁用' : '已启用')
  } catch (e: any) {
    showToast('err', e.message || '操作失败')
  } finally {
    togglingDisabled.value[u.userId] = false
  }
}

async function resetPin(u: AdminUser) {
  if (!confirm(`重置「${u.userName}」(${u.userNumber}) 的 PIN？\n这会强制他的所有会话登出。`)) return
  resetting.value = true
  try {
    const r = await adminApi.resetUserPin(u.userId)
    newPinResult.value = { user: `${u.userName} / ${u.userNumber}`, pin: r.newPin }
  } catch (e: any) {
    showToast('err', e.message || '重置失败')
  } finally {
    resetting.value = false
  }
}

async function copyPin() {
  if (!newPinResult.value) return
  const ok = await copyText(newPinResult.value.pin)
  showToast(ok ? 'ok' : 'err', ok ? 'PIN 已复制' : '复制失败，请手动选取')
}

async function remove(u: AdminUser) {
  if (!confirm(`删除用户「${u.userName}」(${u.userNumber})？\n会同时释放他的邀请码。`)) return
  try {
    await adminApi.deleteUser(u.userId)
    showToast('ok', '已删除')
    if (drawerUserId.value === u.userId) drawerUserId.value = null
    await load()
  } catch (e: any) {
    showToast('err', e.message || '删除失败')
  }
}

// --- token refresh modal ---

async function openRefresh(u: AdminUser) {
  refreshTarget.value = u
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

async function copyRefreshAuthorizationUrl() {
  const ok = await copyText(rAuthorizationUrl.value)
  showToast(ok ? 'ok' : 'err', ok ? '授权链接已复制' : '复制失败')
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
    showToast('ok', `Token 已刷新（${refreshTarget.value.userName}）`)
    const refreshed = refreshTarget.value
    refreshTarget.value = null
    await load()
    if (refreshed) await refreshStatus(refreshed)
  } catch (e: any) {
    showToast('err', e.message || '刷新失败')
  } finally {
    refreshing.value = false
  }
}

watch(refreshTarget, v => {
  if (!v) rCallback.value = ''
})

// --- list-view helpers ---

function listDayBit(jsDay: number): number {
  return (jsDay + 6) % 7
}
const todayBit = computed(() => listDayBit(now.value.getDay()))

function signsToday(u: AdminUser): boolean {
  return (u.signDays & (1 << todayBit.value)) !== 0
}

const WEEK_LABELS = ['一', '二', '三', '四', '五', '六', '日']

function signDaysSummary(mask: number): string {
  const m = mask & 0x7f
  if (m === 0x7f) return '每天'
  if (m === 0x1f) return '工作日'
  if (m === 0x60) return '周末'
  // Compact representation: list enabled days
  const days = []
  for (let i = 0; i < 7; i++) {
    if (m & (1 << i)) days.push(WEEK_LABELS[i])
  }
  return days.length === 0 ? '从不' : '周' + days.join('、')
}

function shortStatus(s: StatusEntry): { tone: string; text: string } {
  if (s === 'loading') return { tone: 'zinc', text: '…' }
  if (!s) return { tone: 'zinc', text: '—' }
  switch (s.state) {
    case 'signed':
      return { tone: 'emerald', text: '已签' }
    case 'canSign':
      return { tone: 'amber', text: '待签' }
    case 'pending':
      return { tone: 'zinc', text: '未开放' }
    case 'exempt':
      return { tone: 'blue', text: s.exemptReason || '请假/免签' }
    case 'boarding':
      return { tone: 'blue', text: '走读' }
    case 'tokenExpired':
      return { tone: 'red', text: 'Token 失效' }
    case 'error':
      return { tone: 'red', text: '查询失败' }
    default:
      return { tone: 'zinc', text: s.state }
  }
}

function shortStatusClass(tone: string): string {
  switch (tone) {
    case 'emerald':
      return 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-300 ring-1 ring-emerald-500/25'
    case 'amber':
      return 'bg-amber-500/10 text-amber-700 dark:text-amber-300 ring-1 ring-amber-500/30'
    case 'blue':
      return 'bg-blue-500/10 text-blue-700 dark:text-blue-300 ring-1 ring-blue-500/30'
    case 'red':
      return 'bg-red-500/10 text-red-700 dark:text-red-300 ring-1 ring-red-500/30'
    case 'zinc':
    default:
      return 'bg-zinc-500/10 text-zinc-700 dark:text-zinc-300 ring-1 ring-zinc-500/20'
  }
}

function busyFor(u: AdminUser) {
  return {
    sign: !!signing.value[u.userId],
    dorm: !!bindingDorm.value[u.userId],
    auto: !!togglingAuto.value[u.userId],
    days: !!savingDays.value[u.userId],
    disabled: !!togglingDisabled.value[u.userId],
    resetting: resetting.value,
  }
}
</script>

<template>
  <div class="space-y-4">
    <header class="flex flex-col lg:flex-row lg:items-end lg:justify-between gap-3">
      <div>
        <h1 class="text-2xl font-bold tracking-tight">用户</h1>
        <p class="text-sm text-zinc-500 mt-1">
          每个用户的全部配置 + 今晚学校状态 + 手动操作。卡片态详尽，列表态点击进抽屉。
        </p>
      </div>
      <div class="flex items-center gap-2">
        <div class="relative w-full max-w-xs">
          <Search class="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-zinc-500" />
          <input
            v-model="search"
            @keyup.enter="load"
            placeholder="搜索姓名 / 学号 / 邀请码"
            class="w-full pl-9 pr-3 py-2 bg-white/85 dark:bg-zinc-900/60 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-lg text-sm focus-ring text-zinc-900 dark:text-zinc-200 placeholder:text-zinc-400 dark:placeholder:text-zinc-600"
          />
        </div>
        <!-- View-mode toggle -->
        <div class="inline-flex bg-white/85 dark:bg-zinc-900/60 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-lg p-0.5">
          <button
            @click="setViewMode('card')"
            :class="viewMode === 'card'
              ? 'bg-emerald-500 text-zinc-950'
              : 'text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-200'"
            class="inline-flex items-center gap-1 px-2.5 py-1.5 rounded-md text-xs font-medium transition-colors"
            title="卡片态"
          >
            <LayoutGrid class="w-3.5 h-3.5" />
            卡片
          </button>
          <button
            @click="setViewMode('list')"
            :class="viewMode === 'list'
              ? 'bg-emerald-500 text-zinc-950'
              : 'text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-200'"
            class="inline-flex items-center gap-1 px-2.5 py-1.5 rounded-md text-xs font-medium transition-colors"
            title="列表态（紧凑）"
          >
            <ListIcon class="w-3.5 h-3.5" />
            列表
          </button>
        </div>
        <button @click="load" :disabled="loading" title="重新加载 + 重拉学校状态"
          class="text-xs text-zinc-500 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-200 px-2 py-2 rounded-md hover:bg-black/5 dark:hover:bg-white/5 transition-colors inline-flex items-center gap-1">
          <RefreshCw class="w-3.5 h-3.5" :class="loading ? 'wangui-spin' : ''" />
        </button>
      </div>
    </header>

    <!-- Loading / empty -->
    <div
      v-if="loading && users.length === 0"
      class="py-20 flex items-center justify-center"
    >
      <div class="h-6 w-6 rounded-full border-2 border-zinc-800 border-t-emerald-400 wangui-spin" />
    </div>

    <div
      v-else-if="users.length === 0"
      class="rounded-xl bg-white/85 dark:bg-zinc-900/60 ring-1 ring-black/[0.08] dark:ring-white/[0.06] py-16 text-center text-sm text-zinc-500"
    >
      还没有用户
    </div>

    <!-- Card view -->
    <section
      v-else-if="viewMode === 'card'"
      class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-3"
    >
      <UserCard
        v-for="u in users"
        :key="u.userId"
        :user="u"
        :dorms="dorms"
        :status="statusByUser[u.userId]"
        :now="now"
        :busy="busyFor(u)"
        @sign="signNow(u)"
        @change-dorm="(id: number) => changeDorm(u, id)"
        @toggle-auto="toggleAuto(u)"
        @toggle-day="(b: number) => toggleDay(u, b)"
        @toggle-disabled="toggleDisabled(u)"
        @reset-pin="resetPin(u)"
        @refresh-token="openRefresh(u)"
        @refresh-status="refreshStatus(u)"
        @remove="remove(u)"
      />
    </section>

    <!-- List view -->
    <section
      v-else
      class="rounded-xl bg-white/85 dark:bg-zinc-900/60 ring-1 ring-black/[0.08] dark:ring-white/[0.06] overflow-hidden"
    >
      <div class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead class="bg-white/50 dark:bg-zinc-950/50 border-b border-black/[0.08] dark:border-white/[0.06]">
            <tr class="text-left text-[10px] text-zinc-500 uppercase tracking-wide">
              <!-- Header checkbox toggles all -->
              <th class="px-4 py-3 font-medium w-10">
                <button
                  type="button"
                  @click="toggleSelectAll"
                  class="inline-flex items-center justify-center w-4 h-4 text-zinc-500 hover:text-emerald-400 transition-colors"
                  :title="allSelected ? '取消全选' : '全选'"
                >
                  <CheckSquare v-if="allSelected" class="w-4 h-4 text-emerald-400" />
                  <Square v-else-if="someSelected" class="w-4 h-4 text-emerald-400/60" />
                  <Square v-else class="w-4 h-4" />
                </button>
              </th>
              <th class="px-4 py-3 font-medium">用户</th>
              <th class="px-4 py-3 font-medium">学院 / 班级</th>
              <th class="px-4 py-3 font-medium">宿舍楼</th>
              <th class="px-4 py-3 font-medium">自动 / 周次</th>
              <th class="px-4 py-3 font-medium">时刻</th>
              <th class="px-4 py-3 font-medium">学校状态</th>
              <th class="px-4 py-3 font-medium">Token</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-black/[0.05] dark:divide-white/[0.04]">
            <tr
              v-for="u in users"
              :key="u.userId"
              @click="openDrawer(u)"
              class="hover:bg-black/[0.02] dark:hover:bg-white/[0.02] transition-colors cursor-pointer"
              :class="[u.isDisabled ? 'opacity-60' : '', isSelected(u.userId) ? 'bg-emerald-500/[0.06]' : '']"
            >
              <!-- Checkbox (stops row click) -->
              <td class="px-4 py-2.5 w-10" @click.stop="toggleSelect(u.userId)">
                <button
                  type="button"
                  class="inline-flex items-center justify-center w-4 h-4 text-zinc-500 hover:text-emerald-400 transition-colors"
                >
                  <CheckSquare v-if="isSelected(u.userId)" class="w-4 h-4 text-emerald-400" />
                  <Square v-else class="w-4 h-4" />
                </button>
              </td>
              <!-- User -->
              <td class="px-4 py-2.5">
                <div class="flex items-center gap-2.5 min-w-0">
                  <Avatar :src="u.userAvatarUrl" :name="u.userName" :size="32" rounded="lg" />
                  <div class="min-w-0">
                    <div class="flex items-center gap-1.5">
                      <span class="text-sm font-medium truncate">{{ u.userName }}</span>
                      <span
                        v-if="u.isDisabled"
                        class="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium bg-red-500/15 text-red-700 dark:text-red-300 ring-1 ring-red-500/30 shrink-0"
                      >
                        禁
                      </span>
                    </div>
                    <p class="text-[11px] text-zinc-500 font-mono-token truncate">{{ u.userNumber }}</p>
                  </div>
                </div>
              </td>
              <!-- Section / Class -->
              <td class="px-4 py-2.5 text-xs text-zinc-600 dark:text-zinc-400">
                <div class="truncate">{{ u.userSection || '—' }}</div>
                <div class="truncate text-[11px] text-zinc-500">{{ u.userClass || '—' }}</div>
              </td>
              <!-- Dorm -->
              <td class="px-4 py-2.5 text-xs">
                <span
                  v-if="u.dormName"
                  class="inline-flex items-center px-2 py-0.5 rounded-md bg-emerald-500/10 text-emerald-700 dark:text-emerald-300 ring-1 ring-emerald-500/25"
                >
                  {{ u.dormName }}
                </span>
                <span v-else class="text-zinc-500">未绑定</span>
              </td>
              <!-- Auto / SignDays -->
              <td class="px-4 py-2.5 text-xs">
                <div class="flex items-center gap-1.5">
                  <span
                    class="w-1.5 h-1.5 rounded-full"
                    :class="u.autoSign ? 'bg-emerald-500' : 'bg-zinc-500'"
                  />
                  <span :class="u.autoSign ? 'text-emerald-600 dark:text-emerald-300' : 'text-zinc-500'">
                    {{ u.autoSign ? '自动开' : '自动关' }}
                  </span>
                </div>
                <div class="text-[11px] text-zinc-500 mt-0.5">{{ signDaysSummary(u.signDays) }}</div>
              </td>
              <!-- Trigger time -->
              <td class="px-4 py-2.5 text-xs">
                <span
                  class="font-mono-token tabular-nums font-medium"
                  :class="signsToday(u) ? 'text-emerald-600 dark:text-emerald-300' : 'text-zinc-500'"
                >
                  22:{{ String(u.triggerMinute).padStart(2, '0') }}
                </span>
                <div class="text-[10px] text-zinc-500">±{{ u.jitterSec }}s</div>
              </td>
              <!-- School status -->
              <td class="px-4 py-2.5 text-xs">
                <span
                  class="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium"
                  :class="shortStatusClass(shortStatus(statusByUser[u.userId]).tone)"
                >
                  {{ shortStatus(statusByUser[u.userId]).text }}
                </span>
              </td>
              <!-- Token -->
              <td class="px-4 py-2.5">
                <CheckCircle2 v-if="u.tokenValid" class="w-3.5 h-3.5 text-emerald-400 inline" />
                <XCircle v-else class="w-3.5 h-3.5 text-red-400 inline" />
                <span class="ml-1 text-xs text-zinc-500 tabular-nums">
                  {{ formatRemaining(Math.max(0, u.tokenExp - Math.floor(Date.now() / 1000))) }}
                </span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <!-- Floating bulk-action bar — only shown in list view with >0 selected. -->
    <Transition name="bulkbar">
      <div
        v-if="viewMode === 'list' && selectedIds.size > 0"
        class="fixed bottom-24 md:bottom-6 inset-x-0 z-30 flex justify-center pointer-events-none"
      >
        <div class="pointer-events-auto bg-zinc-900/95 dark:bg-zinc-100/95 backdrop-blur ring-1 ring-emerald-500/40 rounded-2xl shadow-2xl px-3 py-2 flex items-center gap-2 max-w-[calc(100vw-1.5rem)]">
          <span class="text-xs font-medium text-zinc-200 dark:text-zinc-800 px-2">
            已选 {{ selectedIds.size }}
          </span>
          <span class="h-5 w-px bg-zinc-700 dark:bg-zinc-300"></span>
          <button
            @click="bulkAction('sign')"
            :disabled="bulkBusy"
            class="inline-flex items-center gap-1 px-2.5 py-1.5 rounded-md text-xs font-medium text-emerald-300 dark:text-emerald-700 hover:bg-emerald-500/15 disabled:opacity-50 transition-colors"
            title="批量立即签到"
          >
            <PlayCircle class="w-3.5 h-3.5" />
            立即签
          </button>
          <button
            @click="bulkAction('enable')"
            :disabled="bulkBusy"
            class="inline-flex items-center gap-1 px-2.5 py-1.5 rounded-md text-xs font-medium text-blue-300 dark:text-blue-700 hover:bg-blue-500/15 disabled:opacity-50 transition-colors"
            title="批量启用"
          >
            <Power class="w-3.5 h-3.5" />
            启用
          </button>
          <button
            @click="bulkAction('disable')"
            :disabled="bulkBusy"
            class="inline-flex items-center gap-1 px-2.5 py-1.5 rounded-md text-xs font-medium text-amber-300 dark:text-amber-700 hover:bg-amber-500/15 disabled:opacity-50 transition-colors"
            title="批量禁用"
          >
            <Power class="w-3.5 h-3.5" />
            禁用
          </button>
          <button
            @click="bulkAction('delete')"
            :disabled="bulkBusy"
            class="inline-flex items-center gap-1 px-2.5 py-1.5 rounded-md text-xs font-medium text-red-300 dark:text-red-700 hover:bg-red-500/15 disabled:opacity-50 transition-colors"
            title="批量删除（不可撤销）"
          >
            <Trash2 class="w-3.5 h-3.5" />
            删除
          </button>
          <span class="h-5 w-px bg-zinc-700 dark:bg-zinc-300"></span>
          <button
            @click="clearSelection"
            class="inline-flex items-center justify-center p-1.5 rounded-md text-zinc-400 dark:text-zinc-500 hover:bg-zinc-700/50 dark:hover:bg-zinc-300/50 transition-colors"
            title="清除选择"
          >
            <X class="w-3.5 h-3.5" />
          </button>
        </div>
      </div>
    </Transition>

    <!-- Drawer: opens when a list row is clicked, shows the full UserCard -->
    <Transition name="drawer">
      <div v-if="drawerUser" class="fixed inset-0 z-50 flex" @click.self="closeDrawer">
        <div class="flex-1 bg-white/70 dark:bg-zinc-950/70 backdrop-blur-sm" @click="closeDrawer" />
        <aside class="w-full max-w-md bg-zinc-100 dark:bg-zinc-900 ring-1 ring-black/10 dark:ring-white/10 overflow-y-auto">
          <div class="sticky top-0 z-10 p-3 bg-zinc-100/95 dark:bg-zinc-900/95 backdrop-blur border-b border-black/[0.08] dark:border-white/[0.06] flex items-center justify-between">
            <span class="text-xs text-zinc-500 truncate">用户详情 · 所有编辑都立即生效</span>
            <button @click="closeDrawer"
              class="text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-200 transition-colors p-1 rounded hover:bg-black/5 dark:hover:bg-white/5">
              <X class="w-4 h-4" />
            </button>
          </div>
          <UserCard
            :user="drawerUser"
            :dorms="dorms"
            :status="statusByUser[drawerUser.userId]"
            :now="now"
            :busy="busyFor(drawerUser)"
            :drawer="true"
            @sign="signNow(drawerUser)"
            @change-dorm="(id: number) => drawerUser && changeDorm(drawerUser, id)"
            @toggle-auto="drawerUser && toggleAuto(drawerUser)"
            @toggle-day="(b: number) => drawerUser && toggleDay(drawerUser, b)"
            @toggle-disabled="drawerUser && toggleDisabled(drawerUser)"
            @reset-pin="drawerUser && resetPin(drawerUser)"
            @refresh-token="drawerUser && openRefresh(drawerUser)"
            @refresh-status="drawerUser && refreshStatus(drawerUser)"
            @remove="drawerUser && remove(drawerUser)"
          />
        </aside>
      </div>
    </Transition>

    <!-- New PIN modal (after admin reset) -->
    <Transition name="modal">
      <div v-if="newPinResult" class="fixed inset-0 z-[60] bg-white/85 dark:bg-zinc-950/85 backdrop-blur flex items-center justify-center p-4"
        @click.self="newPinResult = null">
        <div class="w-full max-w-sm bg-zinc-100 dark:bg-zinc-900 ring-1 ring-emerald-500/30 rounded-2xl shadow-2xl">
          <div class="p-5 border-b border-black/[0.08] dark:border-white/[0.06] flex items-center gap-3">
            <div class="w-10 h-10 rounded-xl bg-emerald-500/15 ring-1 ring-emerald-500/30 flex items-center justify-center shrink-0">
              <KeyRound class="w-5 h-5 text-emerald-400" />
            </div>
            <div>
              <h2 class="text-base font-bold">PIN 已重置</h2>
              <p class="text-xs text-zinc-500 mt-0.5 truncate">{{ newPinResult.user }}</p>
            </div>
          </div>

          <div class="p-5">
            <p class="text-xs text-zinc-500 dark:text-zinc-400 mb-3">把这串 PIN 告诉用户（旧 PIN 已作废，所有会话已登出）：</p>
            <div class="bg-white dark:bg-zinc-950 ring-1 ring-emerald-500/30 rounded-xl px-4 py-5 text-center">
              <p class="text-4xl font-bold font-mono-token tabular-nums tracking-[0.5em] text-emerald-400 pl-[0.5em]">
                {{ newPinResult.pin }}
              </p>
            </div>
            <p class="text-[10px] text-zinc-500 dark:text-zinc-600 text-center mt-2">关闭后此 PIN 不再显示</p>
          </div>

          <div class="px-5 pb-5 flex gap-2">
            <button @click="copyPin"
              class="flex-1 inline-flex items-center justify-center gap-1.5 bg-emerald-500 hover:bg-emerald-400 text-zinc-950 text-sm font-medium py-2 rounded-lg transition-colors">
              <Copy class="w-3.5 h-3.5" />
              复制 PIN
            </button>
            <button @click="newPinResult = null"
              class="px-4 py-2 text-sm text-zinc-500 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-200 transition-colors">
              我已记下
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
              刷新 Token — {{ refreshTarget.userName }}
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
.drawer-enter-active, .drawer-leave-active { transition: opacity 0.2s ease; }
.drawer-enter-active aside, .drawer-leave-active aside { transition: transform 0.25s cubic-bezier(0.4, 0, 0.2, 1); }
.drawer-enter-from, .drawer-leave-to { opacity: 0; }
.drawer-enter-from aside, .drawer-leave-to aside { transform: translateX(100%); }
.modal-enter-active, .modal-leave-active { transition: opacity 0.2s; }
.modal-enter-from, .modal-leave-to { opacity: 0; }
.bulkbar-enter-active, .bulkbar-leave-active { transition: opacity 0.2s ease, transform 0.2s cubic-bezier(0.4, 0, 0.2, 1); }
.bulkbar-enter-from, .bulkbar-leave-to { opacity: 0; transform: translateY(12px); }
</style>
