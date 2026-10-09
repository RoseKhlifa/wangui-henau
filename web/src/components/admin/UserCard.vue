<script setup lang="ts">
import { computed } from 'vue'
import {
  Building2,
  PlayCircle,
  CalendarDays,
  Clock,
  KeyRound,
  RefreshCw,
  History,
  Ticket,
  Power,
  Trash2,
  Bell,
  BellOff,
} from 'lucide-vue-next'
import type { AdminUser, Dorm, SchoolCheckinStatus } from '../../types'
import { formatDateTime } from '../../lib/format'
import Avatar from '../Avatar.vue'

// Card body for one regular admin-managed user. Renders all inline-edit
// controls (dorm picker, autoSign toggle, signDays bitmask, manual
// sign / token refresh / PIN reset / disable / delete) plus the latest
// records and school CheckinStatus chip.
//
// Reused from two places:
//   1. Users.vue grid view — one card per user in a responsive grid
//   2. Users.vue list view — slide-over content when a row is clicked
//
// All actions are emitted up, so the parent owns the state machine
// (busy flags, API calls, toasts, reload). Keeps this component pure.

type StatusEntry = SchoolCheckinStatus | 'loading' | undefined

const props = withDefaults(
  defineProps<{
    user: AdminUser
    dorms: Dorm[]
    status: StatusEntry
    now: Date
    /** Per-action in-flight flags so we can disable controls cleanly. */
    busy?: {
      sign?: boolean
      dorm?: boolean
      auto?: boolean
      days?: boolean
      disabled?: boolean
      resetting?: boolean
    }
    /** When rendered inside a drawer, the outer ring/background is provided
     *  by the drawer wrapper; setting this to true drops the card's own
     *  ring + bg so it doesn't double up. */
    drawer?: boolean
  }>(),
  { busy: () => ({}), drawer: false },
)

const emit = defineEmits<{
  sign: []
  'change-dorm': [number]
  'toggle-auto': []
  'toggle-day': [number]
  'toggle-disabled': []
  'reset-pin': []
  'refresh-token': []
  'refresh-status': []
  remove: []
}>()

const DAY_LABELS = ['一', '二', '三', '四', '五', '六', '日'] as const

function dayBit(jsDay: number): number {
  // Bitmask: 0=Mon..6=Sun. JS Date.getDay: 0=Sun..6=Sat.
  return (jsDay + 6) % 7
}
const todayBit = computed(() => dayBit(props.now.getDay()))

const signsToday = computed(
  () => (props.user.signDays & (1 << todayBit.value)) !== 0,
)
const signTimeStr = computed(
  () => `22:${String(props.user.triggerMinute).padStart(2, '0')}`,
)

const tokenUrgency = computed<'expired' | 'soon' | 'ok'>(() => {
  const cur = Math.floor(props.now.getTime() / 1000)
  if (props.user.tokenExp <= cur) return 'expired'
  if (props.user.tokenExp - cur < 48 * 3600) return 'soon'
  return 'ok'
})

const statusInfo = computed<{ tone: string; label: string; hint: string }>(() => {
  const s = props.status
  if (s === 'loading') return { tone: 'loading', label: '加载中…', hint: '' }
  if (!s) return { tone: 'zinc', label: '未拉取', hint: '' }
  switch (s.state) {
    case 'signed':
      return { tone: 'ok', label: '学校：已签', hint: s.message }
    case 'canSign':
      return { tone: 'amber', label: '学校：待签', hint: s.message }
    case 'pending':
      return { tone: 'zinc', label: '学校：未开放', hint: s.message }
    case 'exempt':
      return {
        tone: 'blue',
        label: '学校：' + (s.exemptReason || s.message || '请假/免签'),
        hint: s.message,
      }
    case 'boarding':
      return { tone: 'blue', label: '学校：走读/外宿', hint: s.message }
    case 'tokenExpired':
      return { tone: 'red', label: 'Token 失效', hint: '需要刷新' }
    case 'error':
      return { tone: 'red', label: '查询失败', hint: s.message }
    default:
      return { tone: 'zinc', label: s.state, hint: s.message }
  }
})

function chipClass(tone: string): string {
  switch (tone) {
    case 'ok':
      return 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-300 ring-1 ring-emerald-500/25'
    case 'amber':
      return 'bg-amber-500/10 text-amber-700 dark:text-amber-300 ring-1 ring-amber-500/30'
    case 'blue':
      return 'bg-blue-500/10 text-blue-700 dark:text-blue-300 ring-1 ring-blue-500/30'
    case 'red':
      return 'bg-red-500/10 text-red-700 dark:text-red-300 ring-1 ring-red-500/30'
    case 'zinc':
      return 'bg-zinc-500/10 text-zinc-700 dark:text-zinc-300 ring-1 ring-zinc-500/25'
    case 'loading':
      return 'bg-zinc-500/5 text-zinc-500 ring-1 ring-black/[0.06] dark:ring-white/[0.06]'
    default:
      return 'bg-zinc-500/10 text-zinc-500 ring-1 ring-zinc-500/20'
  }
}

function recordDotClass(status: string): string {
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

function recordLabel(status: string): string {
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

const u = computed(() => props.user)
</script>

<template>
  <article
    class="flex flex-col overflow-hidden"
    :class="drawer
      ? ''
      : 'rounded-2xl bg-white/85 dark:bg-zinc-900/60 ring-1 ring-black/[0.08] dark:ring-white/[0.06]'"
    :style="u.isDisabled && !drawer ? 'opacity: 0.6' : ''"
  >
    <!-- Header -->
    <header class="p-4 flex items-start gap-3 border-b border-black/[0.05] dark:border-white/[0.04]">
      <Avatar :src="u.userAvatarUrl" :name="u.userName" :size="44" rounded="lg" />
      <div class="min-w-0 flex-1">
        <div class="flex items-center gap-1.5 flex-wrap">
          <h3 class="text-base font-semibold text-zinc-900 dark:text-zinc-100 truncate">
            {{ u.userName }}
          </h3>
          <span
            v-if="u.isDisabled"
            class="shrink-0 inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium bg-red-500/15 text-red-700 dark:text-red-300 ring-1 ring-red-500/30"
          >
            已禁用
          </span>
        </div>
        <p class="text-[11px] text-zinc-500 font-mono-token truncate mt-0.5">{{ u.userNumber }}</p>
        <p
          v-if="u.userSection || u.userClass"
          class="text-[11px] text-zinc-500 truncate"
        >
          {{ u.userSection }}{{ u.userSection && u.userClass ? ' · ' : '' }}{{ u.userClass }}
        </p>
        <p
          v-if="u.inviteCode"
          class="text-[10px] text-zinc-400 dark:text-zinc-500 font-mono-token truncate mt-0.5 inline-flex items-center gap-1"
        >
          <Ticket class="w-2.5 h-2.5" />
          {{ u.inviteCode }}
        </p>
      </div>
      <div class="flex flex-col gap-1 shrink-0">
        <button
          @click="emit('toggle-disabled')"
          :disabled="busy.disabled"
          :title="u.isDisabled ? '启用' : '禁用'"
          class="p-1.5 rounded text-zinc-500 dark:text-zinc-400 hover:bg-amber-500/10 hover:text-amber-400 transition-colors disabled:opacity-50"
        >
          <Power class="w-4 h-4" />
        </button>
        <button
          @click="emit('remove')"
          title="删除"
          class="p-1.5 rounded text-zinc-500 dark:text-zinc-400 hover:bg-red-500/10 hover:text-red-400 transition-colors"
        >
          <Trash2 class="w-4 h-4" />
        </button>
      </div>
    </header>

    <!-- Today's school status (top, prominent) -->
    <div class="px-4 pt-3">
      <div class="flex items-center justify-between gap-2 text-xs">
        <span
          class="inline-flex items-center gap-1.5 px-2 py-1 rounded-md text-[11px] font-medium"
          :class="chipClass(statusInfo.tone)"
          :title="statusInfo.hint || ''"
        >
          <span
            v-if="statusInfo.tone === 'loading'"
            class="h-2.5 w-2.5 rounded-full border-2 border-zinc-400 border-t-transparent wangui-spin"
          />
          <span v-else class="w-1.5 h-1.5 rounded-full bg-current opacity-70" />
          {{ statusInfo.label }}
        </span>
        <button
          @click="emit('refresh-status')"
          class="text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-200 p-1 rounded hover:bg-black/5 dark:hover:bg-white/5 transition-colors"
          title="重拉学校状态"
        >
          <RefreshCw class="w-3 h-3" />
        </button>
      </div>
      <p
        v-if="status && status !== 'loading' && (status as SchoolCheckinStatus).message"
        class="text-[10px] text-zinc-500 dark:text-zinc-400 mt-1 truncate"
      >
        {{ (status as SchoolCheckinStatus).message }}
      </p>
    </div>

    <!-- Body fields -->
    <div class="p-4 space-y-2.5 text-xs flex-1">
      <!-- Dorm picker -->
      <div class="flex items-center gap-2">
        <Building2 class="w-3.5 h-3.5 text-zinc-500 shrink-0" />
        <span class="text-zinc-500 shrink-0 w-14">宿舍楼</span>
        <select
          :value="u.dormId ?? 0"
          :disabled="busy.dorm"
          @change="(e) => emit('change-dorm', +((e.target as HTMLSelectElement).value))"
          class="flex-1 min-w-0 bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-md px-2 py-1 text-xs focus-ring text-zinc-900 dark:text-zinc-200 disabled:opacity-50"
        >
          <option :value="0">— 未绑定 —</option>
          <option v-for="d in dorms" :key="d.id" :value="d.id">{{ d.name }}</option>
        </select>
      </div>

      <!-- Auto sign toggle -->
      <div class="flex items-center gap-2">
        <PlayCircle class="w-3.5 h-3.5 text-zinc-500 shrink-0" />
        <span class="text-zinc-500 shrink-0 w-14">自动签到</span>
        <button
          type="button"
          :disabled="busy.auto"
          @click="emit('toggle-auto')"
          :class="u.autoSign ? 'bg-emerald-500' : 'bg-zinc-300 dark:bg-zinc-700'"
          class="relative inline-flex h-5 w-9 items-center rounded-full transition-colors disabled:opacity-50"
        >
          <span
            :class="u.autoSign ? 'translate-x-4' : 'translate-x-0.5'"
            class="inline-block h-4 w-4 transform rounded-full bg-white shadow transition-transform"
          />
        </button>
        <span class="text-[11px]" :class="u.autoSign ? 'text-emerald-500 dark:text-emerald-300' : 'text-zinc-500'">
          {{ u.autoSign ? '已开启' : '已关闭' }}
        </span>
      </div>

      <!-- SignDays week picker -->
      <div class="flex items-start gap-2">
        <CalendarDays class="w-3.5 h-3.5 text-zinc-500 shrink-0 mt-0.5" />
        <span class="text-zinc-500 shrink-0 w-14 mt-0.5">签到周</span>
        <div class="flex-1 flex flex-wrap items-center gap-1">
          <button
            v-for="(label, i) in DAY_LABELS"
            :key="i"
            type="button"
            :disabled="busy.days"
            @click="emit('toggle-day', i)"
            :title="(u.signDays & (1 << i)) ? '点击关闭周' + label : '点击开启周' + label"
            :class="(u.signDays & (1 << i))
              ? (todayBit === i
                ? 'bg-emerald-500 text-zinc-950 ring-2 ring-emerald-400/40'
                : 'bg-emerald-500/20 text-emerald-700 dark:text-emerald-300 ring-1 ring-emerald-500/30')
              : (todayBit === i
                ? 'bg-amber-500/10 text-amber-700 dark:text-amber-300 ring-2 ring-amber-500/40'
                : 'bg-zinc-200 dark:bg-zinc-800/70 text-zinc-500 dark:text-zinc-500 ring-1 ring-black/[0.06] dark:ring-white/[0.05]')"
            class="w-6 h-6 rounded text-[11px] font-medium transition-all disabled:opacity-50"
          >
            {{ label }}
          </button>
          <span
            v-if="!signsToday"
            class="ml-1 text-[10px] text-amber-500 dark:text-amber-400 font-medium"
          >
            · 今天不签
          </span>
        </div>
      </div>

      <!-- Sign time -->
      <div class="flex items-center gap-2">
        <Clock class="w-3.5 h-3.5 text-zinc-500 shrink-0" />
        <span class="text-zinc-500 shrink-0 w-14">签到时刻</span>
        <span
          class="font-semibold tabular-nums"
          :class="signsToday ? 'text-emerald-500 dark:text-emerald-300' : 'text-zinc-500'"
        >
          {{ signTimeStr }}
        </span>
        <span class="text-[10px] text-zinc-500">±{{ u.jitterSec }}s</span>
      </div>

      <!-- Token row -->
      <div class="flex items-center gap-2">
        <KeyRound class="w-3.5 h-3.5 text-zinc-500 shrink-0" />
        <span class="text-zinc-500 shrink-0 w-14">Token</span>
        <span
          class="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] font-medium shrink-0"
          :class="tokenUrgency === 'expired'
            ? 'bg-red-500/10 text-red-700 dark:text-red-300 ring-1 ring-red-500/25'
            : tokenUrgency === 'soon'
              ? 'bg-amber-500/10 text-amber-700 dark:text-amber-300 ring-1 ring-amber-500/30'
              : 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-300 ring-1 ring-emerald-500/25'"
        >
          <span
            class="w-1 h-1 rounded-full"
            :class="tokenUrgency === 'expired' ? 'bg-red-400' : tokenUrgency === 'soon' ? 'bg-amber-400' : 'bg-emerald-400'"
          />
          {{ tokenUrgency === 'expired' ? '已失效' : tokenUrgency === 'soon' ? '快过期' : '有效' }}
        </span>
        <span class="text-[10px] text-zinc-500 truncate flex-1" :title="formatDateTime(u.tokenExp)">
          {{ formatDateTime(u.tokenExp) }}
        </span>
        <button
          @click="emit('refresh-token')"
          :class="tokenUrgency === 'expired'
            ? 'bg-red-500 hover:bg-red-400 text-white'
            : tokenUrgency === 'soon'
              ? 'bg-amber-500 hover:bg-amber-400 text-zinc-950'
              : 'bg-white/80 dark:bg-zinc-900/80 text-zinc-700 dark:text-zinc-300 ring-1 ring-black/[0.08] dark:ring-white/[0.06] hover:ring-emerald-500/40'"
          class="inline-flex items-center gap-1 px-2 py-0.5 rounded text-[10px] font-medium transition-colors shrink-0"
          title="让本人重新授权以刷新 Token"
        >
          <RefreshCw class="w-3 h-3" />
          刷新
        </button>
      </div>
    </div>

    <!-- Recent records -->
    <div
      v-if="u.recentRecords && u.recentRecords.length > 0"
      class="px-4 pb-3"
    >
      <div class="flex items-center gap-1.5 mb-1.5">
        <History class="w-3 h-3 text-zinc-500" />
        <span class="text-[10px] text-zinc-500 uppercase tracking-wide">最近记录</span>
      </div>
      <ul class="space-y-1">
        <li
          v-for="r in u.recentRecords.slice(0, 3)"
          :key="r.id"
          class="flex items-center gap-2 text-[11px]"
        >
          <span class="w-1.5 h-1.5 rounded-full shrink-0" :class="recordDotClass(r.status)" />
          <span class="shrink-0 text-zinc-700 dark:text-zinc-300 font-medium w-7">{{ recordLabel(r.status) }}</span>
          <span class="text-zinc-500 font-mono-token text-[10px] shrink-0">{{ formatDateTime(r.occurredAt) }}</span>
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
        @click="emit('sign')"
        :disabled="busy.sign"
        class="flex-1 inline-flex items-center justify-center gap-1.5 bg-emerald-500 hover:bg-emerald-400 disabled:opacity-50 text-zinc-950 text-xs font-medium px-3 py-1.5 rounded-md transition-colors"
        title="代签到（应急 / 测试）"
      >
        <PlayCircle class="w-3.5 h-3.5" />
        {{ busy.sign ? '签到中…' : '立即签到' }}
      </button>
      <button
        @click="emit('reset-pin')"
        :disabled="busy.resetting"
        class="inline-flex items-center justify-center gap-1 px-3 py-1.5 rounded-md text-xs text-zinc-700 dark:text-zinc-300 bg-white/80 dark:bg-zinc-900/80 ring-1 ring-black/[0.08] dark:ring-white/[0.06] hover:ring-emerald-500/40 transition-colors"
        title="重置 PIN"
      >
        <KeyRound class="w-3.5 h-3.5" />
        PIN
      </button>
    </footer>
  </article>
</template>
