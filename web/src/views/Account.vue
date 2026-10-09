<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import QRCode from 'qrcode'
import {
  KeyRound,
  Ticket,
  Copy,
  AlertTriangle,
  ChevronDown,
  Trash2,
  CheckCircle2,
  XCircle,
  ShieldCheck,
  AlertCircle,
  QrCode,
  RefreshCw,
} from 'lucide-vue-next'
import { useAuth } from '../stores/auth'
import { api, type SchoolOAuthPrepared } from '../api'
import { formatDateTime, formatRemaining, tokenProgressPercent, tokenProgressColor } from '../lib/format'
import { showToast } from '../lib/toast'
import { copyText } from '../lib/clipboard'
import { detectSchoolOauthInput } from '../lib/schoolOauth'

const router = useRouter()
const route = useRoute()
const auth = useAuth()

const newToken = ref('')
const callbackUrl = ref('')
const savingToken = ref(false)
const tokenSectionRef = ref<HTMLElement | null>(null)
const tokenPrefilledFlash = ref(false)
const showLegacyToken = ref(false)
const wechatQrDataUrl = ref('')
const wechatAttemptId = ref('')
const wechatAuthorizationUrl = ref('')
const buildingWechatQr = ref(false)
const callbackLooksValid = computed(
  () => detectSchoolOauthInput(callbackUrl.value).kind === 'callback-url',
)

const oldPin = ref('')
const newPinA = ref('')
const newPinB = ref('')
const savingPin = ref(false)
const pinDigits = (s: string) => s.replace(/\D/g, '').slice(0, 6)
const isPinValid = (s: string) => /^\d{4,6}$/.test(s)
const pinMismatch = computed(
  () => newPinA.value && newPinB.value && newPinA.value !== newPinB.value,
)
const canSavePin = computed(
  () =>
    !savingPin.value &&
    oldPin.value.length > 0 &&
    isPinValid(newPinA.value) &&
    newPinA.value === newPinB.value,
)

const showDanger = ref(false)
const confirmText = ref('')
const deleting = ref(false)

onMounted(async () => {
  await auth.init()
  // tokengrab → Login.vue hands a refreshed token to us via sessionStorage
  // (URL would put it in browser history; query strings are visible). Pick
  // it up, drop it into the textarea, flash + scroll the section into view.
  await refreshWechatQr()
  if (route.query.prefill === 'token') {
    const tok = sessionStorage.getItem('wangui:prefill_token')
    if (tok) {
      newToken.value = tok
      showLegacyToken.value = true
      sessionStorage.removeItem('wangui:prefill_token')
      tokenPrefilledFlash.value = true
      // Drop the query so refresh doesn't re-trigger the flash.
      router.replace({ path: '/account' })
      await nextTick()
      tokenSectionRef.value?.scrollIntoView({ behavior: 'smooth', block: 'center' })
      window.setTimeout(() => (tokenPrefilledFlash.value = false), 2400)
    }
  }
})

const me = computed(() => auth.state.me)

const RADIUS = 38
const CIRC = 2 * Math.PI * RADIUS
const percent = computed(() => tokenProgressPercent(me.value?.token.remainingSec ?? 0))
const dashOffset = computed(() => CIRC * (1 - percent.value / 100))
const ringColor = computed(() => {
  const s = me.value?.token.remainingSec ?? 0
  if (s <= 0) return 'rgb(82 82 91)'
  if (s < 24 * 3600) return 'rgb(239 68 68)'
  if (s < 3 * 24 * 3600) return 'rgb(245 158 11)'
  return 'rgb(16 185 129)'
})

async function savePin() {
  if (!canSavePin.value) return
  savingPin.value = true
  try {
    await api.changePin(oldPin.value, newPinA.value)
    showToast('ok', 'PIN 已更新')
    oldPin.value = ''
    newPinA.value = ''
    newPinB.value = ''
  } catch (e: any) {
    showToast('err', e.message || 'PIN 更新失败')
  } finally {
    savingPin.value = false
  }
}

async function saveToken() {
  const tok = newToken.value.trim()
  const cb = callbackUrl.value.trim()
  if (!tok && !cb) return
  savingToken.value = true
  try {
    await api.updateToken({
      token: tok || undefined,
      callbackUrl: cb || undefined,
      oauthAttemptId: wechatAttemptId.value || undefined,
    })
    showToast('ok', 'Token 已更新')
    newToken.value = ''
    callbackUrl.value = ''
    await auth.refresh()
  } catch (e: any) {
    showToast('err', e.message || 'Token 更新失败')
  } finally {
    savingToken.value = false
  }
}

async function refreshWechatQr() {
  buildingWechatQr.value = true
  try {
    const prepared = await api.prepareTokenOAuth()
    await applyWechatPrepared(prepared)
  } catch (e: any) {
    wechatAttemptId.value = ''
    wechatAuthorizationUrl.value = ''
    wechatQrDataUrl.value = ''
    showToast('err', e?.message || '学校登录准备失败')
  } finally {
    buildingWechatQr.value = false
  }
}

async function applyWechatPrepared(prepared: SchoolOAuthPrepared) {
  wechatAttemptId.value = prepared.attemptId
  wechatAuthorizationUrl.value = prepared.authorizationUrl
  wechatQrDataUrl.value = await QRCode.toDataURL(prepared.authorizationUrl, {
      width: 240,
      margin: 1,
      errorCorrectionLevel: 'M',
  })
}

async function copyWechatUrl() {
  const ok = await copyText(wechatAuthorizationUrl.value)
  showToast(ok ? 'ok' : 'err', ok ? '授权链接已复制' : '复制失败')
}

async function copyInvite() {
  if (!me.value?.inviteCode) return
  const ok = await copyText(me.value.inviteCode)
  showToast(ok ? 'ok' : 'err', ok ? '邀请码已复制' : '复制失败，请手动选取')
}

async function confirmDelete() {
  if (confirmText.value !== '注销账号') {
    showToast('err', '请输入「注销账号」确认')
    return
  }
  deleting.value = true
  try {
    await api.deleteMe()
    auth.clear()
    showToast('ok', '账号已注销')
    router.push('/login')
  } catch (e: any) {
    showToast('err', e.message || '注销失败')
  } finally {
    deleting.value = false
  }
}

async function logout() {
  try { await api.logout() } catch {}
  auth.clear()
  router.push('/login')
}
</script>

<template>
  <div v-if="me" class="space-y-3">
    <header class="mb-1">
      <h1 class="text-2xl font-bold tracking-tight">账号</h1>
      <p class="text-sm text-zinc-500 mt-1">Token、邀请码、注销操作。</p>
    </header>

    <!-- Token 状态 -->
    <section
      ref="tokenSectionRef"
      :class="tokenPrefilledFlash ? 'ring-emerald-500/60 shadow-[0_0_24px_rgba(16,185,129,0.25)]' : 'ring-black/[0.08] dark:ring-white/[0.06]'"
      class="rounded-xl bg-white/85 dark:bg-zinc-900/60 ring-1 p-5 transition-shadow duration-300"
    >
      <div class="flex items-center justify-between mb-4">
        <div class="flex items-center gap-2">
          <KeyRound class="w-4 h-4 text-zinc-500" />
          <h2 class="text-base font-semibold text-zinc-900 dark:text-zinc-200">学校 Token</h2>
        </div>
        <span
          v-if="me.token.isValid"
          class="inline-flex items-center gap-1 px-2 py-0.5 rounded-md text-xs font-medium bg-emerald-500/15 text-emerald-400 ring-1 ring-emerald-500/30"
        >
          <CheckCircle2 class="w-3 h-3" />
          有效
        </span>
        <span
          v-else
          class="inline-flex items-center gap-1 px-2 py-0.5 rounded-md text-xs font-medium bg-red-500/15 text-red-400 ring-1 ring-red-500/30"
        >
          <XCircle class="w-3 h-3" />
          已失效
        </span>
      </div>

      <div class="flex items-center gap-5">
        <div class="relative w-24 h-24 shrink-0">
          <svg class="w-24 h-24 -rotate-90" viewBox="0 0 100 100">
            <circle cx="50" cy="50" :r="RADIUS" fill="none" stroke="currentColor" stroke-width="6"
              class="text-zinc-200 dark:text-zinc-800" />
            <circle cx="50" cy="50" :r="RADIUS" fill="none"
              :stroke="ringColor" stroke-width="6" stroke-linecap="round"
              :stroke-dasharray="CIRC" :stroke-dashoffset="dashOffset"
              class="transition-[stroke-dashoffset,stroke] duration-700 ease-out" />
          </svg>
          <div class="absolute inset-0 flex flex-col items-center justify-center">
            <div class="text-2xl font-bold tabular-nums leading-none">
              {{ Math.floor(me.token.remainingSec / 86400) }}
            </div>
            <div class="text-[10px] text-zinc-500 mt-1 tracking-wide uppercase">天</div>
          </div>
        </div>
        <div class="flex-1 min-w-0">
          <p class="text-xs text-zinc-500 tracking-wide uppercase">剩余</p>
          <p class="text-base font-medium mt-0.5">{{ formatRemaining(me.token.remainingSec) }}</p>
          <p class="text-xs text-zinc-500 mt-2.5 tracking-wide uppercase">过期时间</p>
          <p class="text-xs tabular-nums text-zinc-700 dark:text-zinc-300 mt-0.5 font-mono-token">
            {{ formatDateTime(me.token.expiresAt) }}
          </p>
        </div>
      </div>

      <div class="mt-5 pt-5 border-t border-black/[0.06] dark:border-white/[0.05]">
        <div class="flex items-center justify-between mb-2">
          <p class="text-xs text-zinc-500 dark:text-zinc-400">电脑微信授权更新学校 Token</p>
          <Transition name="fade">
            <span
              v-if="tokenPrefilledFlash"
              class="inline-flex items-center gap-1 text-[11px] text-emerald-400 font-medium"
            >
              <CheckCircle2 class="w-3 h-3" />
              已自动填入待更新的 JWT
            </span>
          </Transition>
        </div>
        <div class="rounded-xl bg-white/70 dark:bg-zinc-950/70 ring-1 ring-black/[0.05] dark:ring-white/[0.04] p-3">
          <div class="flex flex-col sm:flex-row gap-4">
            <div class="shrink-0 self-center sm:self-start">
              <div class="w-36 h-36 rounded-xl bg-white ring-1 ring-black/[0.06] p-2 flex items-center justify-center overflow-hidden">
                <img
                  v-if="wechatQrDataUrl"
                  :src="wechatQrDataUrl"
                alt="本轮微信授权链接二维码"
                  class="w-full h-full object-contain"
                />
                <div v-else class="text-[11px] text-zinc-400 text-center px-2">
                  {{ buildingWechatQr ? '生成中…' : '二维码生成失败' }}
                </div>
              </div>
            </div>
            <div class="min-w-0 flex-1">
              <p class="text-[11px] text-zinc-500 dark:text-zinc-400 leading-relaxed">
                复制授权链接并在<strong>电脑微信</strong>中打开。看到学校图片页后复制完整 URL，粘贴到下面。本轮约 5 分钟有效，纯 code 无法兑换。
              </p>
              <div class="mt-3 flex flex-wrap gap-2">
                <button
                  type="button"
                  @click="copyWechatUrl"
                  class="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[11px] text-zinc-700 dark:text-zinc-300 bg-white/80 dark:bg-zinc-900/80 ring-1 ring-black/[0.06] dark:ring-white/[0.05] hover:ring-emerald-500/40 transition-colors"
                >
                  <Copy class="w-3 h-3" />
                  复制授权链接
                </button>
                <button
                  type="button"
                  @click="refreshWechatQr"
                  class="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[11px] text-zinc-700 dark:text-zinc-300 bg-white/80 dark:bg-zinc-900/80 ring-1 ring-black/[0.06] dark:ring-white/[0.05] hover:ring-emerald-500/40 transition-colors"
                >
                  <RefreshCw class="w-3 h-3" />
                  重新生成本轮链接
                </button>
              </div>
            </div>
          </div>
        </div>
        <label class="flex items-center gap-1.5 text-xs text-zinc-500 dark:text-zinc-400 mb-1.5 mt-3">
          <QrCode class="w-3.5 h-3.5" />
          学校图片页完整回调链接
        </label>
        <textarea
          v-model="callbackUrl"
          placeholder="https://xhbcs.henau.edu.cn/农大白.png?code=EXAMPLE&state=EXAMPLE"
          class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-lg px-3 py-2 h-24 resize-none focus-ring text-zinc-900 dark:text-zinc-200 placeholder:text-zinc-400 dark:placeholder:text-zinc-600"
        />
        <button
          @click="showLegacyToken = !showLegacyToken"
          type="button"
          class="mt-2 text-[11px] text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-200 transition-colors"
        >
          {{ showLegacyToken ? '收起手动 JWT 兜底' : '高级选项：手动粘贴 JWT' }}
        </button>
        <Transition name="expand">
          <div
            v-if="showLegacyToken"
            class="mt-2 overflow-hidden rounded-lg bg-white/70 dark:bg-zinc-950/70 ring-1 ring-black/[0.05] dark:ring-white/[0.04] p-3"
          >
            <p class="text-[11px] text-zinc-500 dark:text-zinc-400 mb-2">仅在授权流程异常时使用，直接粘贴学校 JWT。</p>
            <textarea
              v-model="newToken"
              placeholder="eyJ..."
              class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-lg px-3 py-2 font-mono-token h-24 resize-none focus-ring text-zinc-900 dark:text-zinc-200 placeholder:text-zinc-400 dark:placeholder:text-zinc-600"
            />
          </div>
        </Transition>
        <button
          @click="saveToken"
          :disabled="savingToken || (!newToken.trim() && !callbackLooksValid)"
          class="mt-2 bg-emerald-500 hover:bg-emerald-400 disabled:bg-zinc-200 dark:disabled:bg-zinc-800 disabled:text-zinc-500 text-zinc-950 text-sm font-medium px-5 py-2 rounded-lg transition-colors disabled:cursor-not-allowed"
        >
          {{ savingToken ? '保存中…' : '更新 Token' }}
        </button>
      </div>
    </section>

    <!-- 修改 PIN -->
    <section class="rounded-xl bg-white/85 dark:bg-zinc-900/60 ring-1 ring-black/[0.08] dark:ring-white/[0.06] p-5">
      <div class="flex items-center gap-2 mb-4">
        <ShieldCheck class="w-4 h-4 text-zinc-500" />
        <h2 class="text-base font-semibold text-zinc-900 dark:text-zinc-200">登录 PIN</h2>
      </div>
      <p class="text-xs text-zinc-500 mb-4">用于学号 + PIN 登录。4–6 位数字。</p>
      <div class="space-y-3">
        <div>
          <label class="block text-[10px] text-zinc-500 tracking-wide uppercase mb-1">当前 PIN</label>
          <input
            :value="oldPin"
            @input="(e: any) => (oldPin = pinDigits(e.target.value))"
            type="password"
            inputmode="numeric"
            autocomplete="current-password"
            maxlength="6"
            placeholder="••••"
            class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-lg px-3 py-2 text-sm focus-ring text-zinc-900 dark:text-zinc-200 placeholder:text-zinc-300 dark:placeholder:text-zinc-700 font-mono-token tracking-[0.4em] text-center"
          />
        </div>
        <div class="grid grid-cols-2 gap-3">
          <div>
            <label class="block text-[10px] text-zinc-500 tracking-wide uppercase mb-1">新 PIN</label>
            <input
              :value="newPinA"
              @input="(e: any) => (newPinA = pinDigits(e.target.value))"
              type="password"
              inputmode="numeric"
              autocomplete="new-password"
              maxlength="6"
              placeholder="4–6 位"
              class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-lg px-3 py-2 text-sm focus-ring text-zinc-900 dark:text-zinc-200 placeholder:text-zinc-300 dark:placeholder:text-zinc-700 font-mono-token tracking-[0.4em] text-center"
            />
          </div>
          <div>
            <label class="block text-[10px] text-zinc-500 tracking-wide uppercase mb-1">再输一次</label>
            <input
              :value="newPinB"
              @input="(e: any) => (newPinB = pinDigits(e.target.value))"
              type="password"
              inputmode="numeric"
              maxlength="6"
              placeholder="重复"
              class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-lg px-3 py-2 text-sm focus-ring text-zinc-900 dark:text-zinc-200 placeholder:text-zinc-300 dark:placeholder:text-zinc-700 font-mono-token tracking-[0.4em] text-center"
            />
          </div>
        </div>
        <p
          v-if="pinMismatch"
          class="text-[11px] text-amber-400 inline-flex items-center gap-1"
        >
          <AlertCircle class="w-3 h-3" />
          两次输入的 PIN 不一致
        </p>
        <button
          @click="savePin"
          :disabled="!canSavePin"
          class="bg-emerald-500 hover:bg-emerald-400 disabled:bg-zinc-200 dark:disabled:bg-zinc-800 disabled:text-zinc-500 text-zinc-950 text-sm font-medium px-5 py-2 rounded-lg transition-colors disabled:cursor-not-allowed"
        >
          {{ savingPin ? '保存中…' : '更新 PIN' }}
        </button>
      </div>
    </section>

    <!-- 邀请码 -->
    <section class="rounded-xl bg-white/85 dark:bg-zinc-900/60 ring-1 ring-black/[0.08] dark:ring-white/[0.06] p-5">
      <div class="flex items-center gap-2 mb-4">
        <Ticket class="w-4 h-4 text-zinc-500" />
        <h2 class="text-base font-semibold text-zinc-900 dark:text-zinc-200">绑定的邀请码</h2>
      </div>
      <div class="flex items-center justify-between gap-3 px-4 py-3 rounded-lg bg-white/70 dark:bg-zinc-950/70 ring-1 ring-black/[0.05] dark:ring-white/[0.04]">
        <span class="font-mono-token text-base text-zinc-900 dark:text-zinc-200 tracking-wider">
          {{ me.inviteCode || '—' }}
        </span>
        <button
          @click="copyInvite"
          class="text-xs text-zinc-500 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-100 px-2 py-1 rounded-md hover:bg-black/5 dark:hover:bg-white/5 transition-colors inline-flex items-center gap-1.5"
        >
          <Copy class="w-3.5 h-3.5" />
          复制
        </button>
      </div>
      <p class="text-xs text-zinc-500 mt-3">
        这张邀请码永久绑定到你的学号 <span class="font-mono-token">{{ me.userNumber }}</span>，注销账号会同时释放邀请码。
      </p>
    </section>

    <!-- 退出 / 危险区 -->
    <section class="rounded-xl bg-white/85 dark:bg-zinc-900/60 ring-1 ring-black/[0.08] dark:ring-white/[0.06] p-5">
      <button
        @click="logout"
        class="w-full text-sm text-zinc-700 dark:text-zinc-300 hover:text-zinc-900 dark:hover:text-zinc-100 transition-colors py-2"
      >
        退出登录
      </button>
    </section>

    <!-- Danger zone -->
    <section class="rounded-2xl bg-red-500/[0.04] ring-1 ring-red-500/20 p-5">
      <button
        @click="showDanger = !showDanger"
        class="w-full flex items-center justify-between"
      >
        <div class="flex items-center gap-2">
          <AlertTriangle class="w-4 h-4 text-red-400" />
          <h2 class="text-sm font-semibold text-red-300">危险区域</h2>
        </div>
        <ChevronDown class="w-4 h-4 text-red-400/60 transition-transform" :class="showDanger ? 'rotate-180' : ''" />
      </button>

      <Transition name="expand">
        <div v-if="showDanger" class="mt-4 overflow-hidden">
          <div class="space-y-3 text-sm text-zinc-700 dark:text-zinc-300">
            <p>注销账号会删除你的全部数据，包括：</p>
            <ul class="list-disc list-inside text-zinc-500 dark:text-zinc-400 text-xs space-y-1 pl-2">
              <li>所有签到记录</li>
              <li>保存的位置与配置</li>
              <li>当前的加密 Token</li>
              <li>所有会话</li>
            </ul>
            <p class="text-xs text-zinc-500 dark:text-zinc-400">邀请码会被释放，你可以稍后重新激活，但旧记录不可恢复。</p>
          </div>

          <div class="mt-4">
            <label class="block text-xs text-zinc-500 dark:text-zinc-400 mb-1.5">输入「注销账号」确认</label>
            <input
              v-model="confirmText"
              placeholder="注销账号"
              class="w-full bg-white dark:bg-zinc-950 ring-1 ring-red-500/20 rounded-lg px-3 py-2 text-sm focus-ring focus:!ring-red-500/40 text-zinc-900 dark:text-zinc-200 placeholder:text-zinc-400 dark:placeholder:text-zinc-600"
            />
          </div>

          <button
            @click="confirmDelete"
            :disabled="deleting || confirmText !== '注销账号'"
            class="mt-3 w-full bg-red-500 hover:bg-red-600 disabled:bg-zinc-200 dark:disabled:bg-zinc-800 disabled:text-zinc-500 text-white text-sm font-medium py-2 rounded-lg transition-colors disabled:cursor-not-allowed inline-flex items-center justify-center gap-2"
          >
            <Trash2 class="w-3.5 h-3.5" />
            {{ deleting ? '注销中…' : '永久注销账号' }}
          </button>
        </div>
      </Transition>
    </section>
  </div>
</template>

<style scoped>
.expand-enter-active, .expand-leave-active { transition: all 0.3s ease; max-height: 600px; }
.expand-enter-from, .expand-leave-to { max-height: 0; opacity: 0; overflow: hidden; }
.fade-enter-active, .fade-leave-active { transition: opacity 0.25s ease; }
.fade-enter-from, .fade-leave-to { opacity: 0; }
</style>
