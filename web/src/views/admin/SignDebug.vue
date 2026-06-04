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
  Layers,
  Compass,
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

// Batch-mode results: server runs all 7 presets sequentially, this is the
// array it returns (stops on first ok). One row per attempt.
interface BatchResult {
  label: string
  sentRequest: unknown
  ok: boolean
  httpStatus?: number
  envelopeCode?: number
  envelopeMessage?: string
  envelopeData?: unknown
  rawBody?: string
  error?: string
}
const batchResults = ref<BatchResult[] | null>(null)
const batchRunning = ref(false)

// Coordinate sweep: probe center + 8 directions × 3 radii (50/200/500m) for
// the school's actual geofence center. Returns one row per probe.
interface SweepResult {
  direction: string
  distanceM: number
  latitude: number
  longitude: number
  ok: boolean
  httpStatus?: number
  envelopeCode?: number
  envelopeMessage?: string
}
const sweepResults = ref<SweepResult[] | null>(null)
const sweepCenter = ref<{ lat: number; lng: number } | null>(null)
const sweepRunning = ref(false)

// Sweep radii (metres). 3 input boxes user can tune. Default: close-range.
const sweepRadii = ref<[string, string, string]>(['50', '200', '500'])

const SWEEP_PRESETS: { label: string; radii: [string, string, string]; note: string }[] = [
  { label: '近距 (GPS / 坐标系偏移)', radii: ['50', '200', '500'], note: '50/200/500m，找 GPS 误差或 WGS84→GCJ02 偏移' },
  { label: '中距 (点错隔壁楼)', radii: ['200', '1000', '3000'], note: '0.2/1/3km，找校区内点错位置' },
  { label: '远距 (点错校区)', radii: ['1000', '5000', '20000'], note: '1/5/20km，找完全错误的校区' },
]

async function runSweep() {
  if (!selectedId.value) {
    showToast('err', '请先选择用户')
    return
  }
  if (sweepRunning.value) return
  const radii = sweepRadii.value.map(s => parseInt(s, 10)).filter(n => !isNaN(n) && n > 0 && n <= 100000)
  if (radii.length === 0) {
    showToast('err', '请填写至少 1 个有效半径 (米)')
    return
  }
  if (!confirm(
    `坐标遍历：8 方向 × ${radii.length} 个半径 = ${1 + 8 * radii.length} 个点。\n` +
    `半径：${radii.join(' / ')}m\n\n` +
    `找到学校接受的位置就停。可能触发风控，谨慎使用。\n\n确认继续？`,
  )) return
  sweepRunning.value = true
  sweepResults.value = null
  sweepCenter.value = null
  result.value = null
  batchResults.value = null
  try {
    const body: Record<string, unknown> = { radii }
    const lat = parseNum(form.value.latitude)
    const lng = parseNum(form.value.longitude)
    if (lat !== null) body.latitude = lat
    if (lng !== null) body.longitude = lng
    const res = await adminApi.signDebugSweep(selectedId.value, body)
    sweepResults.value = res.results
    sweepCenter.value = { lat: res.centerLat, lng: res.centerLng }
    const winner = res.results.find(r => r.ok)
    if (winner) {
      showToast('ok', `✅ 找到了：${winner.direction === 'CENTER' ? '中心点' : winner.direction + ' ' + winner.distanceM + 'm'}`)
    } else {
      showToast('err', `全部 ${res.results.length} 个点被拒 —— 围栏更远或锁更严`)
    }
  } catch (e: any) {
    showToast('err', e.message || '坐标遍历失败')
  } finally {
    sweepRunning.value = false
  }
}

function applySweepPreset(p: typeof SWEEP_PRESETS[0]) {
  sweepRadii.value = [...p.radii]
}

// Distinct distances in the most recent sweep result, sorted asc. Used as
// the row labels of the result grid.
const sweepDistances = computed<number[]>(() => {
  if (!sweepResults.value) return []
  const set = new Set<number>()
  for (const r of sweepResults.value) set.add(r.distanceM)
  return Array.from(set).sort((a, b) => a - b)
})

// Direction → degree for visualizing on a clock-face.
const DIR_DEGS: Record<string, number> = {
  N: 0, NE: 45, E: 90, SE: 135, S: 180, SW: 225, W: 270, NW: 315, CENTER: 0,
}

function dirChip(dir: string): string {
  if (dir === 'CENTER') return '●'
  const arrows: Record<string, string> = {
    N: '↑', NE: '↗', E: '→', SE: '↘', S: '↓', SW: '↙', W: '←', NW: '↖',
  }
  return arrows[dir] || dir
}

async function runBatch() {
  if (!selectedId.value) {
    showToast('err', '请先选择用户')
    return
  }
  if (batchRunning.value) return
  if (!confirm('一口气跑 7 个预设？\n\n找到能让学校接受的那一个就停。期间这个用户会有最多 7 次连续 /checkin 请求打到学校，可能短时间内被风控。')) return
  batchRunning.value = true
  batchResults.value = null
  result.value = null
  try {
    // Body: pass form's lat/lng/address fields as overrides (if filled).
    const body: Record<string, unknown> = {}
    const lat = parseNum(form.value.latitude)
    const lng = parseNum(form.value.longitude)
    if (lat !== null) body.latitude = lat
    if (lng !== null) body.longitude = lng
    if (form.value.locationAddress.trim()) body.locationAddress = form.value.locationAddress.trim()
    if (form.value.city.trim()) body.city = form.value.city.trim()
    if (form.value.road.trim()) body.road = form.value.road.trim()
    if (form.value.poi.trim()) body.poi = form.value.poi.trim()
    const res = await adminApi.signDebugBatch(selectedId.value, body)
    batchResults.value = res.results
    const winner = res.results.find(r => r.ok)
    if (winner) {
      showToast('ok', `✅ 找到了：${winner.label}`)
    } else {
      showToast('err', '7 个预设全部失败 —— 需要换思路')
    }
  } catch (e: any) {
    showToast('err', e.message || '批量调试失败')
  } finally {
    batchRunning.value = false
  }
}

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
          <!-- Sweep radii inputs + presets — collapsible to keep the action row clean -->
          <details class="inline-block">
            <summary class="cursor-pointer text-xs text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-200 px-2 py-2">
              半径 ({{ sweepRadii.join('/') }}m)
            </summary>
            <div class="absolute mt-1 right-0 z-10 w-64 bg-white dark:bg-zinc-900 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-lg shadow-xl p-3 space-y-2">
              <p class="text-[10px] text-zinc-500 uppercase tracking-wide">3 个半径 (米)</p>
              <div class="grid grid-cols-3 gap-1">
                <input v-model="sweepRadii[0]" type="text"
                  class="bg-zinc-50 dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded px-2 py-1 text-xs font-mono-token focus-ring text-zinc-900 dark:text-zinc-200" />
                <input v-model="sweepRadii[1]" type="text"
                  class="bg-zinc-50 dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded px-2 py-1 text-xs font-mono-token focus-ring text-zinc-900 dark:text-zinc-200" />
                <input v-model="sweepRadii[2]" type="text"
                  class="bg-zinc-50 dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded px-2 py-1 text-xs font-mono-token focus-ring text-zinc-900 dark:text-zinc-200" />
              </div>
              <p class="text-[10px] text-zinc-500 uppercase tracking-wide pt-1">预设</p>
              <button
                v-for="p in SWEEP_PRESETS"
                :key="p.label"
                type="button"
                @click="applySweepPreset(p)"
                class="w-full text-left px-2 py-1.5 rounded text-[11px] hover:bg-emerald-500/10 text-zinc-700 dark:text-zinc-300"
              >
                <span class="font-medium">{{ p.label }}</span>
                <br />
                <span class="text-[10px] text-zinc-500">{{ p.note }}</span>
              </button>
            </div>
          </details>

          <button
            @click="runSweep"
            :disabled="sweepRunning || !selectedId"
            class="inline-flex items-center gap-1.5 bg-blue-500 hover:bg-blue-400 disabled:opacity-50 text-white text-sm font-medium px-4 py-2 rounded-lg transition-colors"
            title="以当前 lat/lng 为中心，按上面 3 个半径向 8 方向扫描，找学校围栏中心"
          >
            <Compass class="w-3.5 h-3.5" :class="sweepRunning ? 'wangui-spin' : ''" />
            {{ sweepRunning ? '扫描中…' : '经纬度遍历' }}
          </button>
          <button
            @click="runBatch"
            :disabled="batchRunning || !selectedId"
            class="inline-flex items-center gap-1.5 bg-amber-500 hover:bg-amber-400 disabled:opacity-50 text-zinc-950 text-sm font-medium px-4 py-2 rounded-lg transition-colors"
            title="一口气跑全部 7 个字段预设，找到能签到的那个就停"
          >
            <Layers class="w-3.5 h-3.5" :class="batchRunning ? 'wangui-spin' : ''" />
            {{ batchRunning ? '批量中…' : '一键试 7 个' }}
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

    <!-- Sweep results — coordinate grid with hit highlight -->
    <section v-if="sweepResults && sweepResults.length > 0" class="space-y-2">
      <div class="text-xs text-zinc-500 px-1">
        坐标遍历 · 中心点 (<span class="font-mono-token">{{ sweepCenter?.lat.toFixed(6) }}, {{ sweepCenter?.lng.toFixed(6) }}</span>)
        · 试 {{ sweepResults.length }} 个点
        <span v-if="sweepResults.some(r => r.ok)" class="ml-2 text-emerald-500 font-medium">
          ✅ 找到能签的位置
        </span>
        <span v-else class="ml-2 text-red-500 font-medium">
          全部被拒
        </span>
      </div>

      <!-- Quick-glance grid: rows = distance, columns = direction -->
      <div class="rounded-xl bg-white/85 dark:bg-zinc-900/60 ring-1 ring-black/[0.08] dark:ring-white/[0.06] p-4 overflow-x-auto">
        <table class="text-xs tabular-nums w-full">
          <thead>
            <tr class="text-[10px] text-zinc-500 uppercase tracking-wide">
              <th class="px-2 py-1 text-left">距离</th>
              <th v-for="d in ['CENTER','N','NE','E','SE','S','SW','W','NW']" :key="d" class="px-2 py-1 text-center">
                {{ dirChip(d) }}
              </th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="dist in sweepDistances" :key="dist" class="border-t border-black/[0.05] dark:border-white/[0.04]">
              <td class="px-2 py-1.5 text-zinc-500">{{ dist === 0 ? '中心' : dist >= 1000 ? (dist/1000) + 'km' : dist + 'm' }}</td>
              <td v-for="d in ['CENTER','N','NE','E','SE','S','SW','W','NW']" :key="d" class="px-1 py-1 text-center">
                <template v-if="(dist === 0 && d === 'CENTER') || (dist !== 0 && d !== 'CENTER')">
                  <span
                    v-if="sweepResults.find(r => r.direction === d && r.distanceM === dist)"
                    class="inline-flex items-center justify-center w-7 h-6 rounded text-[10px] font-medium"
                    :class="sweepResults.find(r => r.direction === d && r.distanceM === dist)?.ok
                      ? 'bg-emerald-500/30 text-emerald-700 dark:text-emerald-200 ring-2 ring-emerald-500'
                      : sweepResults.find(r => r.direction === d && r.distanceM === dist)?.envelopeMessage?.includes('围栏')
                        ? 'bg-red-500/15 text-red-700 dark:text-red-300 ring-1 ring-red-500/30'
                        : sweepResults.find(r => r.direction === d && r.distanceM === dist)?.envelopeMessage?.includes('打卡时间')
                          ? 'bg-amber-500/15 text-amber-700 dark:text-amber-300 ring-1 ring-amber-500/30'
                          : 'bg-zinc-500/15 text-zinc-500 ring-1 ring-zinc-500/30'"
                    :title="sweepResults.find(r => r.direction === d && r.distanceM === dist)?.envelopeMessage"
                  >
                    {{ sweepResults.find(r => r.direction === d && r.distanceM === dist)?.ok ? '✓' : '✗' }}
                  </span>
                  <span v-else class="text-zinc-400">·</span>
                </template>
                <template v-else>—</template>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- Successful hit detail -->
      <div
        v-for="r in sweepResults.filter(x => x.ok)"
        :key="r.direction + r.distanceM"
        class="rounded-xl bg-emerald-500/[0.08] ring-1 ring-emerald-500/40 p-4 text-sm"
      >
        <div class="flex items-center gap-2 mb-2">
          <CheckCircle2 class="w-4 h-4 text-emerald-500" />
          <span class="font-semibold">✅ 签到成功 · {{ r.direction }} {{ r.distanceM === 0 ? '中心点' : r.distanceM + 'm' }}</span>
        </div>
        <div class="grid grid-cols-2 gap-2 text-xs text-zinc-700 dark:text-zinc-300">
          <div>
            <p class="text-[10px] text-zinc-500 uppercase tracking-wide">学校接受的坐标</p>
            <p class="font-mono-token">{{ r.latitude.toFixed(6) }}, {{ r.longitude.toFixed(6) }}</p>
          </div>
          <div>
            <p class="text-[10px] text-zinc-500 uppercase tracking-wide">距你当前保存坐标</p>
            <p class="font-mono-token">{{ r.distanceM === 0 ? '中心 (无偏移)' : r.distanceM + 'm 向 ' + r.direction }}</p>
          </div>
        </div>
        <div class="mt-3 p-3 rounded-lg bg-white dark:bg-zinc-950 ring-1 ring-emerald-500/20 text-[11px] text-zinc-600 dark:text-zinc-400">
          建议：把宿舍楼管理里这个楼的坐标改成 <span class="font-mono-token text-emerald-500">{{ r.latitude.toFixed(6) }}, {{ r.longitude.toFixed(6) }}</span> 应该就好了。
        </div>
      </div>
    </section>

    <!-- Batch results — one card per preset, OK ones glow green -->
    <section v-if="batchResults && batchResults.length > 0" class="space-y-2">
      <div class="text-xs text-zinc-500 px-1">
        共试 {{ batchResults.length }} 个预设 ·
        <span v-if="batchResults.some(r => r.ok)" class="text-emerald-500 font-medium">
          已找到能签的预设 ✅（学校接受后剩余预设跳过）
        </span>
        <span v-else class="text-red-500 font-medium">7 个预设全部被拒</span>
      </div>
      <article
        v-for="(r, idx) in batchResults"
        :key="idx"
        class="rounded-xl ring-1 overflow-hidden"
        :class="r.ok
          ? 'bg-emerald-500/[0.08] ring-emerald-500/40'
          : 'bg-red-500/[0.05] ring-red-500/30'"
      >
        <header class="px-4 py-2.5 border-b border-black/[0.05] dark:border-white/[0.04] flex items-center gap-2 flex-wrap">
          <CheckCircle2 v-if="r.ok" class="w-4 h-4 text-emerald-500 shrink-0" />
          <XCircle v-else class="w-4 h-4 text-red-500 shrink-0" />
          <h3 class="text-sm font-semibold">{{ r.label }}</h3>
          <span v-if="r.envelopeMessage" class="text-xs text-zinc-500">— {{ r.envelopeMessage }}</span>
          <div class="flex-1"></div>
          <span v-if="r.envelopeCode" class="text-[11px] text-zinc-500 font-mono-token">code={{ r.envelopeCode }}</span>
        </header>
        <details class="px-4 py-3 text-xs">
          <summary class="cursor-pointer text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-200">查看请求体 / 学校响应</summary>
          <div class="mt-2 space-y-2">
            <div>
              <p class="text-[10px] text-zinc-500 tracking-wide uppercase mb-1">sent request</p>
              <pre class="text-[10px] font-mono-token bg-white dark:bg-zinc-950 ring-1 ring-black/[0.06] dark:ring-white/[0.05] rounded-md p-2 overflow-x-auto leading-relaxed">{{ JSON.stringify(r.sentRequest, null, 2) }}</pre>
            </div>
            <div v-if="r.envelopeData !== undefined && r.envelopeData !== null">
              <p class="text-[10px] text-zinc-500 tracking-wide uppercase mb-1">envelope.data</p>
              <pre class="text-[10px] font-mono-token bg-white dark:bg-zinc-950 ring-1 ring-black/[0.06] dark:ring-white/[0.05] rounded-md p-2 overflow-x-auto leading-relaxed">{{ JSON.stringify(r.envelopeData, null, 2) }}</pre>
            </div>
            <div v-if="r.rawBody">
              <p class="text-[10px] text-zinc-500 tracking-wide uppercase mb-1">raw response body</p>
              <pre class="text-[10px] font-mono-token bg-white dark:bg-zinc-950 ring-1 ring-black/[0.06] dark:ring-white/[0.05] rounded-md p-2 overflow-x-auto leading-relaxed">{{ r.rawBody }}</pre>
            </div>
            <div v-if="r.error" class="text-xs text-red-500">{{ r.error }}</div>
          </div>
        </details>
      </article>
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
