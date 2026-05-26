<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  Wrench,
  Search,
  Play,
  EyeOff,
  RefreshCw,
  ArrowDownToLine,
  CheckCircle2,
  XCircle,
} from 'lucide-vue-next'
import type { AdminUser } from '../../types'
import { adminApi } from '../../api'
import { showToast } from '../../lib/toast'

// Diagnostic console for the 2026-05 geofence rejection. Pick a user, tweak
// any field of /checkin's request body, fire it. School's raw response
// (envelope code/message/data + raw body) shows below so admin can iterate
// and find what the new validator wants.

const users = ref<AdminUser[]>([])
const loadingUsers = ref(false)
const selectedId = ref<string>('')

// Form: every field is a string (so the empty input means "don't override").
// Numeric fields get parsed at submit.
const form = ref({
  latitude: '',
  longitude: '',
  accuracy: '',
  altitude: '',
  speed: '',
  coordType: '',
  timestamp: '',
  locationAddress: '',
  city: '',
  road: '',
  poi: '',
  deviceModel: '',
  deviceSystem: '',
  ruleId: '',
})

const dryRun = ref(false)
const running = ref(false)

interface DebugResult {
  sentRequest?: unknown
  dryRun?: boolean
  ok?: boolean
  httpStatus?: number
  envelopeCode?: number
  envelopeMessage?: string
  envelopeData?: unknown
  rawBody?: string
  error?: string
}
const result = ref<DebugResult | null>(null)

async function loadUsers() {
  loadingUsers.value = true
  try {
    users.value = await adminApi.listUsers('', 500)
    if (!selectedId.value && users.value.length > 0) {
      // Pre-select the first non-guest, non-disabled user with a valid token.
      const candidate = users.value.find(u => u.tokenValid && !u.isDisabled)
      if (candidate) selectedId.value = candidate.userId
    }
  } catch (e: any) {
    showToast('err', e.message || '加载失败')
  } finally {
    loadingUsers.value = false
  }
}
onMounted(loadUsers)

const selectedUser = computed(() =>
  users.value.find(u => u.userId === selectedId.value) ?? null,
)

function loadUserDefaults() {
  // Pre-fill form with the user's saved coords. Doesn't include the school's
  // expected geo-fence — that's what we're trying to find.
  const u = selectedUser.value
  if (!u) return
  form.value.latitude = String(u.latitude ?? '')
  form.value.longitude = String(u.longitude ?? '')
  showToast('ok', '已填入该用户当前坐标')
}

function parseNum(s: string): number | null {
  if (!s.trim()) return null
  const n = Number(s)
  return isNaN(n) ? null : n
}
function parseInt64(s: string): number | null {
  return parseNum(s)
}

function buildPayload(): Record<string, unknown> {
  const p: Record<string, unknown> = { dryRun: dryRun.value }
  const lat = parseNum(form.value.latitude)
  const lng = parseNum(form.value.longitude)
  if (lat !== null) p.latitude = lat
  if (lng !== null) p.longitude = lng
  const acc = parseNum(form.value.accuracy)
  if (acc !== null) p.accuracy = acc
  const alt = parseNum(form.value.altitude)
  if (alt !== null) p.altitude = alt
  const spd = parseNum(form.value.speed)
  if (spd !== null) p.speed = spd
  if (form.value.coordType.trim()) p.coordType = form.value.coordType.trim()
  const ts = parseInt64(form.value.timestamp)
  if (ts !== null) p.timestamp = ts
  if (form.value.locationAddress.trim()) p.locationAddress = form.value.locationAddress.trim()
  if (form.value.city.trim()) p.city = form.value.city.trim()
  if (form.value.road.trim()) p.road = form.value.road.trim()
  if (form.value.poi.trim()) p.poi = form.value.poi.trim()
  if (form.value.deviceModel.trim()) p.deviceModel = form.value.deviceModel.trim()
  if (form.value.deviceSystem.trim()) p.deviceSystem = form.value.deviceSystem.trim()
  const rid = parseInt64(form.value.ruleId)
  if (rid !== null) p.ruleId = rid
  return p
}

async function run() {
  if (!selectedId.value) {
    showToast('err', '请先选择用户')
    return
  }
  running.value = true
  result.value = null
  try {
    result.value = await adminApi.signDebug(selectedId.value, buildPayload())
    if (result.value.dryRun) {
      showToast('ok', '已构建请求体（dry-run）')
    } else if (result.value.ok) {
      showToast('ok', '✅ 学校接受了！')
    } else {
      showToast('err', `学校拒绝：${result.value.envelopeMessage || '未知错误'}`)
    }
  } catch (e: any) {
    showToast('err', e.message || '调试失败')
  } finally {
    running.value = false
  }
}

function clearAll() {
  form.value = {
    latitude: '',
    longitude: '',
    accuracy: '',
    altitude: '',
    speed: '',
    coordType: '',
    timestamp: '',
    locationAddress: '',
    city: '',
    road: '',
    poi: '',
    deviceModel: '',
    deviceSystem: '',
    ruleId: '',
  }
  result.value = null
}

// Presets — quickly try common variations from the 2026-05 geofence fix list.
const PRESETS = [
  {
    label: '裸最小（仅 rule+lat+lng）',
    note: '只保留必备字段，看是不是被新加的可选字段卡了',
    apply: () => {
      form.value.accuracy = ''
      form.value.altitude = ''
      form.value.speed = ''
      form.value.coordType = ''
      form.value.timestamp = ''
    },
  },
  {
    label: '加 accuracy=15',
    note: '声明 GPS 精度 15m（一般校园 GPS 应该 ≤ 25m）',
    apply: () => {
      form.value.accuracy = '15'
    },
  },
  {
    label: '声明 coordType=wgs84',
    note: '主动告诉学校我们传的是 WGS84，让其转换',
    apply: () => {
      form.value.coordType = 'wgs84'
    },
  },
  {
    label: '声明 coordType=gcj02',
    note: '假设学校期望 GCJ02 坐标',
    apply: () => {
      form.value.coordType = 'gcj02'
    },
  },
  {
    label: '加 timestamp=now',
    note: '加位置捕获时间（毫秒），防 replay',
    apply: () => {
      form.value.timestamp = String(Date.now())
    },
  },
  {
    label: '完整 wx.getLocation',
    note: 'accuracy + altitude + speed + timestamp 全套（模拟真机）',
    apply: () => {
      form.value.accuracy = '13'
      form.value.altitude = '85'
      form.value.speed = '0'
      form.value.timestamp = String(Date.now())
    },
  },
]
</script>

<template>
  <div class="space-y-4">
    <header>
      <h1 class="text-2xl font-bold tracking-tight flex items-center gap-2">
        <Wrench class="w-5 h-5 text-amber-400" />
        签到调试
      </h1>
      <p class="text-sm text-zinc-500 mt-1">
        排查 2026-05 学校加围栏后的 「当前位置不在签到围栏范围内」 错误。
        选个用户 → 调字段 → 点跑 → 看学校 raw 回应。<strong>不写记录、不发通知</strong>。
      </p>
    </header>

    <!-- User picker -->
    <section class="rounded-2xl bg-white/85 dark:bg-zinc-900/60 ring-1 ring-black/[0.08] dark:ring-white/[0.06] p-4 flex items-center gap-3 flex-wrap">
      <Search class="w-4 h-4 text-zinc-500 shrink-0" />
      <select
        v-model="selectedId"
        class="flex-1 min-w-[200px] bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-md px-2 py-1.5 text-sm focus-ring text-zinc-900 dark:text-zinc-200"
      >
        <option value="">— 选用户 —</option>
        <option
          v-for="u in users"
          :key="u.userId"
          :value="u.userId"
          :disabled="u.isDisabled || !u.tokenValid"
        >
          {{ u.userName }} ({{ u.userNumber }})
          <template v-if="!u.tokenValid"> · token 失效</template>
          <template v-else-if="u.isDisabled"> · 已禁用</template>
          <template v-else-if="u.dormName"> · {{ u.dormName }}</template>
        </option>
      </select>
      <button
        @click="loadUserDefaults"
        :disabled="!selectedUser"
        class="inline-flex items-center gap-1 text-xs text-zinc-700 dark:text-zinc-300 hover:text-emerald-400 px-2 py-1.5 rounded-md hover:bg-emerald-500/10 disabled:opacity-50 transition-colors"
        title="把该用户当前的坐标填到下面表单"
      >
        <ArrowDownToLine class="w-3.5 h-3.5" />
        填入当前坐标
      </button>
      <button
        @click="loadUsers"
        :disabled="loadingUsers"
        class="text-xs text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-200 p-1.5 rounded hover:bg-black/5 dark:hover:bg-white/5"
      >
        <RefreshCw class="w-3.5 h-3.5" :class="loadingUsers ? 'wangui-spin' : ''" />
      </button>
    </section>

    <!-- Form + Presets -->
    <section class="grid grid-cols-1 lg:grid-cols-3 gap-3">
      <!-- Form -->
      <div class="lg:col-span-2 rounded-2xl bg-white/85 dark:bg-zinc-900/60 ring-1 ring-black/[0.08] dark:ring-white/[0.06] p-5 space-y-3">
        <h2 class="text-base font-semibold text-zinc-900 dark:text-zinc-200 mb-1">请求体（留空 = 用用户默认 / 不发字段）</h2>

        <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <div>
            <label class="block text-[10px] text-zinc-500 tracking-wide uppercase mb-1">latitude</label>
            <input v-model="form.latitude" type="text" placeholder="如 34.78912"
              class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-md px-2 py-1.5 text-sm font-mono-token focus-ring text-zinc-900 dark:text-zinc-200" />
          </div>
          <div>
            <label class="block text-[10px] text-zinc-500 tracking-wide uppercase mb-1">longitude</label>
            <input v-model="form.longitude" type="text" placeholder="如 113.65432"
              class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-md px-2 py-1.5 text-sm font-mono-token focus-ring text-zinc-900 dark:text-zinc-200" />
          </div>
          <div>
            <label class="block text-[10px] text-zinc-500 tracking-wide uppercase mb-1">accuracy <span class="normal-case text-amber-400">推测</span></label>
            <input v-model="form.accuracy" type="text" placeholder="米，如 15"
              class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-md px-2 py-1.5 text-sm font-mono-token focus-ring text-zinc-900 dark:text-zinc-200" />
          </div>
          <div>
            <label class="block text-[10px] text-zinc-500 tracking-wide uppercase mb-1">altitude <span class="normal-case text-amber-400">推测</span></label>
            <input v-model="form.altitude" type="text" placeholder="米，如 80"
              class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-md px-2 py-1.5 text-sm font-mono-token focus-ring text-zinc-900 dark:text-zinc-200" />
          </div>
          <div>
            <label class="block text-[10px] text-zinc-500 tracking-wide uppercase mb-1">speed <span class="normal-case text-amber-400">推测</span></label>
            <input v-model="form.speed" type="text" placeholder="m/s, 一般 0"
              class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-md px-2 py-1.5 text-sm font-mono-token focus-ring text-zinc-900 dark:text-zinc-200" />
          </div>
          <div>
            <label class="block text-[10px] text-zinc-500 tracking-wide uppercase mb-1">coordType <span class="normal-case text-amber-400">推测</span></label>
            <input v-model="form.coordType" type="text" placeholder="wgs84 / gcj02 / bd09"
              class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-md px-2 py-1.5 text-sm font-mono-token focus-ring text-zinc-900 dark:text-zinc-200" />
          </div>
          <div>
            <label class="block text-[10px] text-zinc-500 tracking-wide uppercase mb-1">timestamp ms <span class="normal-case text-amber-400">推测</span></label>
            <input v-model="form.timestamp" type="text" placeholder="毫秒，Date.now()"
              class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-md px-2 py-1.5 text-sm font-mono-token focus-ring text-zinc-900 dark:text-zinc-200" />
          </div>
          <div>
            <label class="block text-[10px] text-zinc-500 tracking-wide uppercase mb-1">ruleId</label>
            <input v-model="form.ruleId" type="text" placeholder="默认 1"
              class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-md px-2 py-1.5 text-sm font-mono-token focus-ring text-zinc-900 dark:text-zinc-200" />
          </div>
        </div>

        <details class="text-xs">
          <summary class="text-zinc-500 hover:text-zinc-700 dark:hover:text-zinc-300 cursor-pointer">地址字段 / 设备字段 (展开)</summary>
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-3 mt-3">
            <div>
              <label class="block text-[10px] text-zinc-500 tracking-wide uppercase mb-1">locationAddress</label>
              <input v-model="form.locationAddress" type="text"
                class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-md px-2 py-1.5 text-sm focus-ring" />
            </div>
            <div>
              <label class="block text-[10px] text-zinc-500 tracking-wide uppercase mb-1">city</label>
              <input v-model="form.city" type="text"
                class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-md px-2 py-1.5 text-sm focus-ring" />
            </div>
            <div>
              <label class="block text-[10px] text-zinc-500 tracking-wide uppercase mb-1">road</label>
              <input v-model="form.road" type="text"
                class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-md px-2 py-1.5 text-sm focus-ring" />
            </div>
            <div>
              <label class="block text-[10px] text-zinc-500 tracking-wide uppercase mb-1">poi</label>
              <input v-model="form.poi" type="text"
                class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-md px-2 py-1.5 text-sm focus-ring" />
            </div>
            <div>
              <label class="block text-[10px] text-zinc-500 tracking-wide uppercase mb-1">deviceModel</label>
              <input v-model="form.deviceModel" type="text" placeholder="iPhone"
                class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-md px-2 py-1.5 text-sm focus-ring" />
            </div>
            <div>
              <label class="block text-[10px] text-zinc-500 tracking-wide uppercase mb-1">deviceSystem</label>
              <input v-model="form.deviceSystem" type="text" placeholder="iOS"
                class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-md px-2 py-1.5 text-sm focus-ring" />
            </div>
          </div>
        </details>

        <div class="flex items-center gap-3 pt-2">
          <label class="inline-flex items-center gap-1.5 text-xs text-zinc-700 dark:text-zinc-300">
            <input v-model="dryRun" type="checkbox" class="accent-emerald-500" />
            <EyeOff class="w-3.5 h-3.5" />
            Dry-run (不真调学校，只看请求体)
          </label>
          <div class="flex-1"></div>
          <button
            @click="clearAll"
            class="text-xs text-zinc-500 hover:text-red-400 transition-colors"
          >
            清空
          </button>
          <button
            @click="run"
            :disabled="running || !selectedId"
            class="inline-flex items-center gap-1.5 bg-emerald-500 hover:bg-emerald-400 disabled:opacity-50 text-zinc-950 text-sm font-medium px-4 py-2 rounded-lg transition-colors"
          >
            <Play class="w-3.5 h-3.5" :class="running ? 'wangui-spin' : ''" />
            {{ running ? '调试中…' : '跑一次' }}
          </button>
        </div>
      </div>

      <!-- Presets sidebar -->
      <aside class="rounded-2xl bg-white/85 dark:bg-zinc-900/60 ring-1 ring-black/[0.08] dark:ring-white/[0.06] p-5">
        <h2 class="text-base font-semibold text-zinc-900 dark:text-zinc-200 mb-3">推测预设</h2>
        <ul class="space-y-2">
          <li v-for="(p, i) in PRESETS" :key="i">
            <button
              @click="p.apply"
              class="w-full text-left px-3 py-2 rounded-lg bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] hover:ring-emerald-500/40 transition-colors"
            >
              <p class="text-xs font-medium text-zinc-900 dark:text-zinc-200">{{ p.label }}</p>
              <p class="text-[10px] text-zinc-500 mt-0.5 leading-relaxed">{{ p.note }}</p>
            </button>
          </li>
        </ul>
      </aside>
    </section>

    <!-- Result -->
    <section
      v-if="result"
      class="rounded-2xl ring-1 overflow-hidden"
      :class="result.ok
        ? 'bg-emerald-500/[0.05] ring-emerald-500/30'
        : result.dryRun
          ? 'bg-zinc-500/[0.05] ring-zinc-500/30'
          : 'bg-red-500/[0.05] ring-red-500/30'"
    >
      <header class="px-5 py-3 border-b border-black/[0.05] dark:border-white/[0.04] flex items-center gap-2">
        <CheckCircle2 v-if="result.ok" class="w-4 h-4 text-emerald-500" />
        <EyeOff v-else-if="result.dryRun" class="w-4 h-4 text-zinc-500" />
        <XCircle v-else class="w-4 h-4 text-red-500" />
        <h2 class="text-sm font-semibold">
          {{ result.dryRun ? 'Dry-run · 请求体' : result.ok ? '✅ 学校接受' : `❌ 拒绝 · code=${result.envelopeCode}` }}
        </h2>
        <span v-if="result.envelopeMessage" class="text-xs text-zinc-500">— {{ result.envelopeMessage }}</span>
        <div class="flex-1"></div>
        <span v-if="result.httpStatus" class="text-[11px] text-zinc-500 font-mono-token">HTTP {{ result.httpStatus }}</span>
      </header>
      <div class="p-5 space-y-3">
        <div>
          <p class="text-[10px] text-zinc-500 tracking-wide uppercase mb-1">发送的 request body</p>
          <pre class="text-[11px] font-mono-token bg-white dark:bg-zinc-950 ring-1 ring-black/[0.06] dark:ring-white/[0.05] rounded-md p-3 overflow-x-auto leading-relaxed">{{ JSON.stringify(result.sentRequest, null, 2) }}</pre>
        </div>
        <div v-if="result.envelopeData !== undefined && result.envelopeData !== null">
          <p class="text-[10px] text-zinc-500 tracking-wide uppercase mb-1">envelope.data (学校额外信息)</p>
          <pre class="text-[11px] font-mono-token bg-white dark:bg-zinc-950 ring-1 ring-black/[0.06] dark:ring-white/[0.05] rounded-md p-3 overflow-x-auto leading-relaxed">{{ JSON.stringify(result.envelopeData, null, 2) }}</pre>
        </div>
        <div v-if="result.rawBody">
          <p class="text-[10px] text-zinc-500 tracking-wide uppercase mb-1">raw response body</p>
          <pre class="text-[11px] font-mono-token bg-white dark:bg-zinc-950 ring-1 ring-black/[0.06] dark:ring-white/[0.05] rounded-md p-3 overflow-x-auto leading-relaxed">{{ result.rawBody }}</pre>
        </div>
        <div v-if="result.error" class="text-xs text-red-500">
          错误：{{ result.error }}
        </div>
      </div>
    </section>
  </div>
</template>
