<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  Plus,
  Trash2,
  Pencil,
  Search,
  RefreshCw,
  X,
  Building2,
  Users as UsersIcon,
  MapPin,
  Eye,
  EyeOff,
  Info,
} from 'lucide-vue-next'
import type { AdminDorm, DormUserBrief } from '../../types'
import { adminApi } from '../../api'
import { formatDateTime } from '../../lib/format'
import { showToast } from '../../lib/toast'

const dorms = ref<AdminDorm[]>([])
const loading = ref(false)
const search = ref('')

// Users-in-dorm modal state
const usersDorm = ref<AdminDorm | null>(null)
const dormUsers = ref<DormUserBrief[]>([])
const loadingDormUsers = ref(false)

async function openDormUsers(d: AdminDorm) {
  usersDorm.value = d
  dormUsers.value = []
  loadingDormUsers.value = true
  try {
    dormUsers.value = await adminApi.dormUsers(d.id)
  } catch (e: any) {
    showToast('err', e.message || '加载用户失败')
  } finally {
    loadingDormUsers.value = false
  }
}

// Modal state
const showModal = ref(false)
const editing = ref<AdminDorm | null>(null)
const form = ref({
  name: '',
  latitude: 0,
  longitude: 0,
  address: '',
  city: '',
  road: '',
  poi: '',
  note: '',
  sendAddressFields: false,
})
const saving = ref(false)

async function load() {
  loading.value = true
  try {
    dorms.value = await adminApi.listDorms()
  } catch (e: any) {
    showToast('err', e.message || '加载失败')
  } finally {
    loading.value = false
  }
}
onMounted(load)

const filtered = computed(() => {
  const q = search.value.trim()
  if (!q) return dorms.value
  return dorms.value.filter(
    d =>
      d.name.includes(q) ||
      d.address.includes(q) ||
      (d.note && d.note.includes(q)),
  )
})

function openCreate() {
  editing.value = null
  form.value = {
    name: '',
    latitude: 0,
    longitude: 0,
    address: '',
    city: '',
    road: '',
    poi: '',
    note: '',
    sendAddressFields: false,
  }
  showModal.value = true
}

function openEdit(d: AdminDorm) {
  editing.value = d
  form.value = {
    name: d.name,
    latitude: d.latitude,
    longitude: d.longitude,
    address: d.address,
    city: d.city,
    road: d.road,
    poi: d.poi,
    note: d.note,
    sendAddressFields: d.sendAddressFields,
  }
  showModal.value = true
}

async function save() {
  if (!form.value.name.trim()) {
    showToast('err', '名称必填')
    return
  }
  if (!form.value.latitude || !form.value.longitude) {
    showToast('err', '请在地图上选点')
    return
  }
  saving.value = true
  try {
    if (editing.value) {
      await adminApi.updateDorm(editing.value.id, form.value)
      showToast('ok', '已更新')
    } else {
      await adminApi.createDorm(form.value)
      showToast('ok', '已添加')
    }
    showModal.value = false
    await load()
  } catch (e: any) {
    showToast('err', e.message || '保存失败')
  } finally {
    saving.value = false
  }
}

async function remove(d: AdminDorm) {
  if (d.users > 0) {
    showToast('err', `已有 ${d.users} 个用户绑定，不能删除`)
    return
  }
  if (!confirm(`删除宿舍楼「${d.name}」？此操作不可恢复。`)) return
  try {
    await adminApi.deleteDorm(d.id)
    showToast('ok', '已删除')
    await load()
  } catch (e: any) {
    showToast('err', e.message || '删除失败')
  }
}
</script>

<template>
  <div class="space-y-3">
    <header class="flex flex-col sm:flex-row sm:items-end sm:justify-between gap-3">
      <div>
        <h1 class="text-2xl font-bold tracking-tight">宿舍楼管理</h1>
        <p class="text-sm text-zinc-500 mt-1">维护用户可选的签到位置。</p>
      </div>
      <button
        @click="openCreate"
        class="self-start inline-flex items-center gap-1.5 bg-emerald-500 hover:bg-emerald-400 text-zinc-950 text-sm font-medium px-4 py-2 rounded-lg transition-colors"
      >
        <Plus class="w-4 h-4" />
        添加宿舍楼
      </button>
    </header>

    <div class="flex items-center gap-3">
      <div class="relative flex-1 max-w-md">
        <Search class="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-zinc-500" />
        <input
          v-model="search"
          placeholder="搜索名称 / 地址 / 备注"
          class="w-full pl-9 pr-3 py-2 bg-white/85 dark:bg-zinc-900/60 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-lg text-sm focus-ring text-zinc-900 dark:text-zinc-200 placeholder:text-zinc-400 dark:placeholder:text-zinc-600"
        />
      </div>
      <button @click="load" :disabled="loading"
        class="text-xs text-zinc-500 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-200 px-2 py-1.5 rounded-md hover:bg-black/5 dark:hover:bg-white/5 transition-colors inline-flex items-center gap-1">
        <RefreshCw class="w-3.5 h-3.5" :class="loading ? 'wangui-spin' : ''" />
      </button>
    </div>

    <section class="rounded-xl bg-white/85 dark:bg-zinc-900/60 ring-1 ring-black/[0.08] dark:ring-white/[0.06] overflow-hidden">
      <div class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead class="bg-white/50 dark:bg-zinc-950/50 border-b border-black/[0.08] dark:border-white/[0.06]">
            <tr class="text-left text-[10px] text-zinc-500 uppercase tracking-wide">
              <th class="px-4 py-3 font-medium">名称</th>
              <th class="px-4 py-3 font-medium">坐标 (WGS84)</th>
              <th class="px-4 py-3 font-medium">地址</th>
              <th class="px-4 py-3 font-medium">签到载荷</th>
              <th class="px-4 py-3 font-medium">用户数</th>
              <th class="px-4 py-3 font-medium">创建</th>
              <th class="px-4 py-3 font-medium text-right">操作</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-black/[0.05] dark:divide-white/[0.04]">
            <tr v-if="loading && filtered.length === 0">
              <td colspan="7" class="px-4 py-10 text-center">
                <div class="h-5 w-5 rounded-full border-2 border-zinc-800 border-t-emerald-400 wangui-spin mx-auto" />
              </td>
            </tr>
            <tr v-else-if="filtered.length === 0">
              <td colspan="7" class="px-4 py-12 text-center text-sm text-zinc-500">
                还没有宿舍楼，点右上角「添加宿舍楼」
              </td>
            </tr>
            <tr v-for="d in filtered" :key="d.id"
              class="hover:bg-black/[0.02] dark:hover:bg-white/[0.02] transition-colors">
              <td class="px-4 py-3 font-medium">
                <div class="flex items-center gap-2">
                  <Building2 class="w-3.5 h-3.5 text-emerald-400" />
                  {{ d.name }}
                </div>
                <div v-if="d.note" class="text-[10px] text-zinc-500 mt-0.5 ml-5">{{ d.note }}</div>
              </td>
              <td class="px-4 py-3 font-mono-token text-zinc-500 dark:text-zinc-400 text-xs tabular-nums">
                {{ d.latitude.toFixed(6) }}, {{ d.longitude.toFixed(6) }}
              </td>
              <td class="px-4 py-3 text-zinc-500 dark:text-zinc-400 text-xs max-w-xs truncate">
                {{ d.address || '—' }}
              </td>
              <td class="px-4 py-3">
                <span
                  v-if="d.sendAddressFields"
                  class="inline-flex items-center gap-1 px-2 py-0.5 rounded-md text-xs bg-blue-500/15 text-blue-300 ring-1 ring-blue-500/30"
                  title="签到时一并发送 locationAddress/city/road/poi"
                >
                  <Eye class="w-3 h-3" />
                  含地址
                </span>
                <span
                  v-else
                  class="inline-flex items-center gap-1 px-2 py-0.5 rounded-md text-xs bg-zinc-200/60 dark:bg-zinc-800/60 text-zinc-500 dark:text-zinc-400 ring-1 ring-black/[0.05] dark:ring-white/[0.04]"
                  title="签到时仅发送 ruleId/latitude/longitude（最小载荷）"
                >
                  <EyeOff class="w-3 h-3" />
                  仅坐标
                </span>
              </td>
              <td class="px-4 py-3">
                <button
                  v-if="d.users > 0"
                  @click.stop="openDormUsers(d)"
                  class="inline-flex items-center gap-1 px-2 py-0.5 rounded-md text-xs bg-emerald-500/15 text-emerald-700 dark:text-emerald-300 ring-1 ring-emerald-500/30 hover:bg-emerald-500/25 transition-colors cursor-pointer"
                  title="点击查看绑定用户"
                >
                  <UsersIcon class="w-3 h-3" />
                  {{ d.users }}
                </button>
                <span v-else
                  class="inline-flex items-center gap-1 px-2 py-0.5 rounded-md text-xs bg-zinc-200/60 dark:bg-zinc-800/60 text-zinc-500"
                >
                  <UsersIcon class="w-3 h-3" />
                  0
                </span>
              </td>
              <td class="px-4 py-3 text-xs text-zinc-500 tabular-nums">
                {{ formatDateTime(d.createdAt) }}
              </td>
              <td class="px-4 py-3 text-right">
                <div class="inline-flex gap-0.5">
                  <button @click="openEdit(d)" title="编辑"
                    class="p-1.5 rounded hover:bg-black/5 dark:hover:bg-white/5 text-zinc-500 dark:text-zinc-400 hover:text-emerald-400 transition-colors">
                    <Pencil class="w-3.5 h-3.5" />
                  </button>
                  <button @click="remove(d)" :disabled="d.users > 0"
                    :title="d.users > 0 ? '有用户绑定，不能删除' : '删除'"
                    class="p-1.5 rounded hover:bg-black/5 dark:hover:bg-white/5 text-zinc-500 dark:text-zinc-400 hover:text-red-400 disabled:hover:text-zinc-700 disabled:cursor-not-allowed transition-colors">
                    <Trash2 class="w-3.5 h-3.5" />
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <!-- Add / Edit modal -->
    <Transition name="modal">
      <div v-if="showModal" class="fixed inset-0 z-50 bg-white/80 dark:bg-zinc-950/80 backdrop-blur flex items-center justify-center p-4 overflow-y-auto"
        @click.self="showModal = false">
        <div class="w-full max-w-2xl bg-zinc-100 dark:bg-zinc-900 ring-1 ring-black/10 dark:ring-white/10 rounded-2xl shadow-2xl my-8">
          <div class="p-5 border-b border-black/[0.08] dark:border-white/[0.06] flex items-center justify-between">
            <h2 class="text-base font-bold flex items-center gap-2">
              <Building2 class="w-4 h-4 text-emerald-400" />
              {{ editing ? '编辑宿舍楼' : '添加宿舍楼' }}
            </h2>
            <button @click="showModal = false"
              class="text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-200 transition-colors">
              <X class="w-4 h-4" />
            </button>
          </div>

          <div class="p-5 space-y-4">
            <div>
              <label class="block text-xs text-zinc-500 dark:text-zinc-400 mb-1.5">宿舍楼名称 *</label>
              <input v-model="form.name" placeholder='例如「东区 12 号楼」'
                class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-lg px-3 py-2 text-sm focus-ring text-zinc-900 dark:text-zinc-200 placeholder:text-zinc-400 dark:placeholder:text-zinc-600" />
            </div>

            <!-- WGS84 coordinates — manual entry. We used to have a Leaflet
                 MapPicker here but the page's CSP killed every external tile
                 host. We just give the admin a labelled form + how-to. -->
            <div class="rounded-lg bg-blue-500/[0.05] ring-1 ring-blue-500/20 p-3 text-[11px] text-zinc-700 dark:text-zinc-300 leading-relaxed space-y-2">
              <p>
                <strong class="text-blue-300">怎么填经纬度</strong>：必须是 <strong>WGS84</strong>（GPS 通用格式），6 位小数 ≈ 米级精度。形如
                <code class="bg-zinc-200/70 dark:bg-zinc-800/70 px-1 rounded font-mono-token">34.000000, 113.000000</code>（仅为格式示例）。
              </p>
              <p>
                <strong class="text-emerald-300">推荐取法</strong>（按好用程度排序）：
              </p>
              <ol class="list-decimal list-inside space-y-1.5 pl-2">
                <li>
                  <strong>Bing 地图（首推 · 卫星图 + WGS84 + 国内可用）</strong><br/>
                  <a href="https://www.bing.com/maps" target="_blank" rel="noopener noreferrer"
                    class="text-emerald-400 hover:underline">bing.com/maps</a>
                  → 右上角图层切「鸟瞰」(卫星) → 缩到能看清屋顶 → <strong>右键</strong>想要的位置 → 弹出气泡显示 6 位经纬度
                </li>
                <li>
                  <strong>iPhone 自带「地图」（有 iPhone 的话最快）</strong><br/>
                  打开「地图」→ 切到「卫星」模式 → <strong>长按</strong>位置掉大头针 → 滑出详情卡 → 看「经纬度」一行 (6 位)
                </li>
                <li>
                  <strong>Apple Maps 网页版</strong><br/>
                  <a href="https://beta.maps.apple.com" target="_blank" rel="noopener noreferrer"
                    class="text-emerald-400 hover:underline">beta.maps.apple.com</a>
                  → 切卫星 → 右键位置 → 显示坐标
                </li>
                <li>
                  <strong>实地拿（最准）</strong><br/>
                  装「GPS Toolbox」(iOS) / 「GPS 状态」(Android) → 到要标的楼下打开 → 显示当前 WGS84
                </li>
              </ol>
              <p class="text-amber-400 mt-2">
                ⚠ <strong>不要从这些工具抄</strong>：百度地图 / 高德地图 / 腾讯地图 / 微信定位 / QQ 浏览器 / 国内任何标注「火星坐标」的服务 —— 它们用的是 <strong>GCJ02 / BD09 加密坐标</strong>，跟 WGS84 偏 100–500 米，直接填会让签到坐标对不上学校系统。
              </p>
              <p class="text-amber-400/70 mt-1 text-[10px]">
                附：天地图卫星层默认右下角坐标显示精度不够（只有 2 位小数），别用。
              </p>
            </div>

            <div class="grid grid-cols-2 gap-3">
              <div>
                <label class="block text-[10px] text-zinc-500 tracking-wide uppercase mb-1">纬度 (latitude) *</label>
                <input v-model.number="form.latitude" type="number" step="0.000001" placeholder="34.000000"
                  class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-lg px-3 py-2 text-sm font-mono-token focus-ring text-zinc-900 dark:text-zinc-200 placeholder:text-zinc-400 dark:placeholder:text-zinc-600" />
                <p class="text-[10px] text-zinc-500 mt-1">南北方向，中国大陆 ~18 到 53</p>
              </div>
              <div>
                <label class="block text-[10px] text-zinc-500 tracking-wide uppercase mb-1">经度 (longitude) *</label>
                <input v-model.number="form.longitude" type="number" step="0.000001" placeholder="113.000000"
                  class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-lg px-3 py-2 text-sm font-mono-token focus-ring text-zinc-900 dark:text-zinc-200 placeholder:text-zinc-400 dark:placeholder:text-zinc-600" />
                <p class="text-[10px] text-zinc-500 mt-1">东西方向，中国大陆 ~73 到 135</p>
              </div>
              <div class="col-span-2">
                <label class="block text-[10px] text-zinc-500 tracking-wide uppercase mb-1">详细地址（可选）</label>
                <input v-model="form.address" placeholder="如「示例城市示例路 12 号示例校区」"
                  class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-lg px-3 py-2 text-sm focus-ring text-zinc-900 dark:text-zinc-200 placeholder:text-zinc-400 dark:placeholder:text-zinc-600" />
              </div>
              <div>
                <label class="block text-[10px] text-zinc-500 tracking-wide uppercase mb-1">城市（可选）</label>
                <input v-model="form.city" placeholder="如「许昌」"
                  class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-lg px-3 py-2 text-sm focus-ring text-zinc-900 dark:text-zinc-200 placeholder:text-zinc-400 dark:placeholder:text-zinc-600" />
              </div>
              <div>
                <label class="block text-[10px] text-zinc-500 tracking-wide uppercase mb-1">街道（可选）</label>
                <input v-model="form.road" placeholder="如「建设路」"
                  class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-lg px-3 py-2 text-sm focus-ring text-zinc-900 dark:text-zinc-200 placeholder:text-zinc-400 dark:placeholder:text-zinc-600" />
              </div>
              <div class="col-span-2">
                <label class="block text-[10px] text-zinc-500 tracking-wide uppercase mb-1">POI 名称（可选）</label>
                <input v-model="form.poi" placeholder="如「东区 12 号楼」"
                  class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-lg px-3 py-2 text-sm focus-ring text-zinc-900 dark:text-zinc-200 placeholder:text-zinc-400 dark:placeholder:text-zinc-600" />
              </div>
              <div class="col-span-2">
                <label class="block text-[10px] text-zinc-500 tracking-wide uppercase mb-1">备注 (仅管理员可见)</label>
                <input v-model="form.note" placeholder='例如「主要给示例学院使用」'
                  class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-lg px-3 py-2 text-sm focus-ring text-zinc-900 dark:text-zinc-200 placeholder:text-zinc-400 dark:placeholder:text-zinc-600" />
              </div>

              <!-- Payload mode toggle -->
              <div class="col-span-2 mt-2 rounded-lg bg-white/50 dark:bg-zinc-950/50 ring-1 ring-black/[0.05] dark:ring-white/[0.04] p-3">
                <div class="flex items-center justify-between gap-3 mb-2">
                  <div class="flex items-center gap-1.5 min-w-0">
                    <Info class="w-3.5 h-3.5 text-zinc-500 shrink-0" />
                    <span class="text-xs text-zinc-700 dark:text-zinc-300 font-medium">签到载荷</span>
                  </div>
                  <button
                    type="button"
                    @click="form.sendAddressFields = !form.sendAddressFields"
                    :class="form.sendAddressFields ? 'bg-blue-500' : 'bg-zinc-300 dark:bg-zinc-700'"
                    class="relative w-10 h-5 rounded-full transition-colors shrink-0"
                  >
                    <span
                      :class="form.sendAddressFields ? 'translate-x-5' : 'translate-x-0.5'"
                      class="absolute top-0.5 left-0 w-4 h-4 bg-white rounded-full shadow-md transition-transform"
                    />
                  </button>
                </div>
                <p class="text-[11px] text-zinc-500 leading-relaxed">
                  <template v-if="form.sendAddressFields">
                    <span class="text-blue-300">含地址</span>：签到请求体一并发送
                    <code class="bg-zinc-200/70 dark:bg-zinc-800/70 px-1 rounded text-zinc-700 dark:text-zinc-300">locationAddress / city / road / poi</code>。
                    跟学校前端真实流量一致，但要求上面填的地址跟坐标精确对应。
                  </template>
                  <template v-else>
                    <span class="text-zinc-700 dark:text-zinc-300">仅坐标</span>：签到请求体只发送
                    <code class="bg-zinc-200/70 dark:bg-zinc-800/70 px-1 rounded text-zinc-700 dark:text-zinc-300">ruleId / latitude / longitude / deviceModel / deviceSystem</code>。
                    经测试足以让签到成功；偏离真实流量结构但避免地址字段对不齐被审计。
                  </template>
                </p>
              </div>
            </div>
          </div>

          <div class="px-5 pb-5 flex justify-end gap-2">
            <button @click="showModal = false"
              class="px-4 py-2 text-sm text-zinc-500 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-200 transition-colors">
              取消
            </button>
            <button @click="save" :disabled="saving"
              class="bg-emerald-500 hover:bg-emerald-400 disabled:opacity-50 text-zinc-950 text-sm font-medium px-5 py-2 rounded-lg transition-colors">
              {{ saving ? '保存中…' : (editing ? '保存修改' : '添加') }}
            </button>
          </div>
        </div>
      </div>
    </Transition>

    <!-- Bound-users modal -->
    <Transition name="modal">
      <div v-if="usersDorm" class="fixed inset-0 z-[60] bg-white/85 dark:bg-zinc-950/85 backdrop-blur flex items-center justify-center p-4"
        @click.self="usersDorm = null">
        <div class="w-full max-w-md bg-zinc-100 dark:bg-zinc-900 ring-1 ring-black/10 dark:ring-white/10 rounded-2xl shadow-2xl">
          <div class="p-5 border-b border-black/[0.08] dark:border-white/[0.06] flex items-center justify-between">
            <div class="flex items-center gap-3 min-w-0">
              <div class="w-9 h-9 rounded-xl bg-emerald-500/15 ring-1 ring-emerald-500/30 flex items-center justify-center shrink-0">
                <Building2 class="w-4 h-4 text-emerald-400" />
              </div>
              <div class="min-w-0">
                <h2 class="text-base font-bold truncate">{{ usersDorm.name }}</h2>
                <p class="text-xs text-zinc-500 mt-0.5">{{ usersDorm.users }} 个绑定用户</p>
              </div>
            </div>
            <button @click="usersDorm = null"
              class="text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-200 transition-colors">
              <X class="w-4 h-4" />
            </button>
          </div>

          <div class="p-5 max-h-[60vh] overflow-y-auto">
            <div v-if="loadingDormUsers" class="flex justify-center py-8">
              <div class="h-5 w-5 rounded-full border-2 border-zinc-300 dark:border-zinc-800 border-t-emerald-400 wangui-spin" />
            </div>
            <p v-else-if="dormUsers.length === 0" class="text-sm text-zinc-500 text-center py-6">
              暂无用户
            </p>
            <ul v-else class="space-y-1.5">
              <li v-for="u in dormUsers" :key="u.userId"
                class="flex items-center justify-between gap-3 px-3 py-2 rounded-lg bg-white dark:bg-zinc-950 ring-1 ring-black/[0.04] dark:ring-white/[0.04]">
                <div class="min-w-0 flex-1">
                  <p class="text-sm font-medium truncate">{{ u.userName }}</p>
                  <p class="text-[11px] text-zinc-500 mt-0.5 truncate">
                    <span class="font-mono-token">{{ u.userNumber }}</span>
                    <span class="mx-1.5 text-zinc-400">·</span>
                    <span>{{ u.userSection }}</span>
                    <span class="mx-1.5 text-zinc-400">·</span>
                    <span>{{ u.userClass }}</span>
                  </p>
                </div>
                <span v-if="u.isDisabled"
                  class="inline-flex px-2 py-0.5 rounded-md text-[10px] bg-red-500/15 text-red-400 ring-1 ring-red-500/30 shrink-0">
                  已禁用
                </span>
                <span v-else-if="u.autoSign"
                  class="inline-flex px-2 py-0.5 rounded-md text-[10px] bg-emerald-500/15 text-emerald-400 ring-1 ring-emerald-500/30 shrink-0">
                  自动
                </span>
                <span v-else
                  class="inline-flex px-2 py-0.5 rounded-md text-[10px] bg-zinc-300/50 dark:bg-zinc-700/50 text-zinc-500 shrink-0">
                  手动
                </span>
              </li>
            </ul>
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
