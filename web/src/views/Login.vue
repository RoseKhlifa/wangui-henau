<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import QRCode from 'qrcode'
import {
  ArrowRight,
  AlertCircle,
  Shield,
  Eye,
  Lock,
  UserCheck,
  X,
  Check,
  Ticket,
  KeyRound,
  Hash,
  ShieldCheck,
  QrCode,
  RefreshCw,
  Copy,
} from 'lucide-vue-next'
import Logo from '../components/Logo.vue'
import { api, type SchoolOAuthPrepared } from '../api'
import { useAuth } from '../stores/auth'
import { showToast } from '../lib/toast'
import { copyText } from '../lib/clipboard'
import { detectSchoolOauthInput } from '../lib/schoolOauth'

const router = useRouter()
const route = useRoute()
const auth = useAuth()

type Mode = 'login' | 'activate'
const mode = ref<Mode>('login')

// Activation is two-step: 'credentials' (invite + PIN + disclaimer) → server
// precheck → 'token' (wechat QR + paste callback). Reason: random visitors
// without an invite never see the OAuth UI, so they can't crib the flow.
type ActivateStep = 'credentials' | 'token'
const activateStep = ref<ActivateStep>('credentials')
const precheckLoading = ref(false)

// Login fields
const loginNumber = ref('')
const loginPin = ref('')

// Activate fields
const inviteCode = ref('')
const token = ref('')
const callbackUrl = ref('')
const pinA = ref('')
const pinB = ref('')
const agreed = ref(localStorage.getItem('wangui:disclaimer') === 'yes')
const showDisclaimerDetail = ref(false)
const showLegacyToken = ref(false)
const wechatQrDataUrl = ref('')
const wechatAttemptId = ref('')
const wechatAuthorizationUrl = ref('')
const buildingWechatQr = ref(false)

// Disclaimer must be expanded for a few seconds before the user is allowed
// to tick the agreement box. If they've previously agreed (localStorage flag)
// we skip the gate.
const READ_DELAY_MS = 4000
const canCheckAgreement = ref(agreed.value)
const readCountdown = ref(0)
let readTickInterval: ReturnType<typeof setInterval> | null = null
let readUnlockTimer: ReturnType<typeof setTimeout> | null = null

function startReadGate() {
  if (canCheckAgreement.value) return
  if (readUnlockTimer) return // already counting
  readCountdown.value = Math.ceil(READ_DELAY_MS / 1000)
  readTickInterval = setInterval(() => {
    readCountdown.value = Math.max(0, readCountdown.value - 1)
  }, 1000)
  readUnlockTimer = setTimeout(() => {
    canCheckAgreement.value = true
    if (readTickInterval) {
      clearInterval(readTickInterval)
      readTickInterval = null
    }
    readUnlockTimer = null
  }, READ_DELAY_MS)
}

function toggleDisclaimer() {
  showDisclaimerDetail.value = !showDisclaimerDetail.value
  if (showDisclaimerDetail.value) startReadGate()
}

onBeforeUnmount(() => {
  if (readUnlockTimer) clearTimeout(readUnlockTimer)
  if (readTickInterval) clearInterval(readTickInterval)
})

const submitting = ref(false)
const error = ref<string | null>(null)

const disclaimerItems = [
  { icon: Shield, color: 'text-zinc-500 dark:text-zinc-400', text: '本工具非官方产品，使用风险由你自行承担。' },
  { icon: Eye, color: 'text-zinc-500 dark:text-zinc-400', text: '开启自动签到即授权服务器在每天 22:00–22:30 期间替你调用学校签到接口。' },
  { icon: X, color: 'text-red-400', text: '你必须保证签到时本人确实在校。不在校时仍让脚本签到等同于谎报位置，后果由你独自承担。' },
  { icon: Lock, color: 'text-zinc-500 dark:text-zinc-400', text: 'Token 在服务器以 AES-GCM 加密存储，PIN 用 bcrypt 哈希存储。' },
  { icon: UserCheck, color: 'text-zinc-500 dark:text-zinc-400', text: '本工具仅向受邀的个人提供，每张邀请码永久绑定到首次使用者。' },
]

const isPinValid = (p: string) => /^\d{4,6}$/.test(p)
const callbackDetection = computed(() => detectSchoolOauthInput(callbackUrl.value))
const callbackLooksValid = computed(() => callbackDetection.value.kind === 'callback-url')

const canSubmit = computed(() => {
  if (submitting.value || precheckLoading.value) return false
  if (mode.value === 'login') {
    return loginNumber.value.trim().length > 0 && isPinValid(loginPin.value)
  }
  // mode === 'activate'
  if (activateStep.value === 'credentials') {
    return (
      inviteCode.value.trim().length > 0 &&
      isPinValid(pinA.value) &&
      pinA.value === pinB.value &&
      agreed.value
    )
  }
  // step === 'token'
  return callbackLooksValid.value || token.value.trim().length > 0
})

const pinMatchHint = computed(() => {
  if (mode.value !== 'activate') return ''
  if (!pinA.value || !pinB.value) return ''
  if (pinA.value === pinB.value) return ''
  return '两次输入的 PIN 不一致'
})

async function submit() {
  error.value = null
  if (!canSubmit.value) return

  // Activate flow, step 1: precheck the invite code BEFORE revealing the
  // wechat-OAuth UI. Random visitors never see step 2 at all.
  if (mode.value === 'activate' && activateStep.value === 'credentials') {
    precheckLoading.value = true
    try {
      const prepared = await api.activatePrecheck(inviteCode.value.trim().toUpperCase())
      activateStep.value = 'token'
      await applyWechatPrepared(prepared)
    } catch (e: any) {
      error.value = e.message || '邀请码校验失败'
    } finally {
      precheckLoading.value = false
    }
    return
  }

  submitting.value = true
  try {
    if (mode.value === 'login') {
      await api.login(loginNumber.value.trim(), loginPin.value)
      showToast('ok', '欢迎回来')
    } else {
      await api.activate(
        inviteCode.value.trim().toUpperCase(),
        pinA.value,
        true,
        {
          token: token.value.trim() || undefined,
          callbackUrl: callbackUrl.value.trim() || undefined,
          oauthAttemptId: wechatAttemptId.value || undefined,
        },
      )
      localStorage.setItem('wangui:disclaimer', 'yes')
      showToast('ok', '激活成功')
    }
    await auth.refresh()
    const redirect = (route.query.redirect as string) || '/'
    router.push(redirect)
  } catch (e: any) {
    error.value = e.message || (mode.value === 'login' ? '登录失败' : '激活失败')
  } finally {
    submitting.value = false
  }
}

function backToCredentials() {
  activateStep.value = 'credentials'
  error.value = null
  // Keep form state (invite / pin / disclaimer); also keep callbackUrl
  // so user doesn't lose paste content if they're toggling.
}

function switchTo(m: Mode) {
  if (mode.value === m) return
  mode.value = m
  error.value = null
  if (m === 'activate') activateStep.value = 'credentials'
}

// Restrict PIN inputs to digits only as user types.
function pinDigits(s: string): string {
  return s.replace(/\D/g, '').slice(0, 6)
}

async function refreshWechatQr() {
  buildingWechatQr.value = true
  try {
    const prepared = await api.activatePrecheck(inviteCode.value.trim().toUpperCase())
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

async function copyWechatAuthorizationUrl() {
  const ok = await copyText(wechatAuthorizationUrl.value)
  showToast(ok ? 'ok' : 'err', ok ? '授权链接已复制' : '复制失败')
}

onMounted(async () => {
  await auth.init()

  // Legacy tokengrab handoff keeps the JWT in the URL fragment so it does not
  // land in Referer headers or server access logs. Format:
  //   #activate=<token>&code=<invite>  (URLSearchParams-style)
  const rawHash = window.location.hash.replace(/^#/, '')
  const params = rawHash ? new URLSearchParams(rawHash) : null
  const tok = params?.get('activate') || ''
  const code = params?.get('code') || ''

  // Wipe the hash so refresh doesn't repopulate, and so the token doesn't
  // linger in the URL bar after the user reads it. Do this before any
  // router navigation so the next page doesn't inherit the hash.
  if (rawHash) {
    history.replaceState(null, '', window.location.pathname + window.location.search)
  }

  if (auth.state.me) {
    // Already logged in.
    //   - With a fresh token in hand → user wants to refresh, not activate.
    //     Hand off to /account via sessionStorage (avoid putting the token
    //     in the URL where it lands in browser history).
    //   - Without → normal redirect to dashboard.
    if (tok) {
      sessionStorage.setItem('wangui:prefill_token', tok)
      router.replace({ path: '/account', query: { prefill: 'token' } })
    } else {
      router.replace('/')
    }
    return
  }

  // Not logged in: pre-fill the activate form.
  if (tok) {
    mode.value = 'activate'
    token.value = tok
    showLegacyToken.value = true
    if (code) inviteCode.value = code
  }
})
</script>

<template>
  <div class="min-h-screen flex items-center justify-center px-4 py-10 relative">
    <div class="w-full max-w-md">
      <div class="flex flex-col items-center mb-8">
        <Logo :size="56" />
        <h1 class="text-3xl font-bold tracking-tight mt-5 text-center">
          勿外传
        </h1>
        <p class="text-sm text-zinc-500 mt-2">内部工具</p>
      </div>

      <div class="bg-white/85 dark:bg-zinc-900/60 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-2xl backdrop-blur-sm overflow-hidden">
        <!-- Tabs -->
        <div class="flex relative border-b border-black/[0.08] dark:border-white/[0.06]">
          <button
            @click="switchTo('login')"
            :class="mode === 'login' ? 'text-zinc-900 dark:text-zinc-100' : 'text-zinc-500 hover:text-zinc-500 dark:hover:text-zinc-400 dark:text-zinc-300'"
            class="flex-1 py-3.5 text-sm font-medium transition-colors relative"
          >
            登录
          </button>
          <button
            @click="switchTo('activate')"
            :class="mode === 'activate' ? 'text-zinc-900 dark:text-zinc-100' : 'text-zinc-500 hover:text-zinc-500 dark:hover:text-zinc-400 dark:text-zinc-300'"
            class="flex-1 py-3.5 text-sm font-medium transition-colors relative"
          >
            激活
          </button>
          <div
            class="absolute bottom-0 h-[2px] bg-emerald-400 transition-all duration-300 ease-out"
            :style="{ left: mode === 'login' ? '0%' : '50%', width: '50%' }"
          />
        </div>

        <!-- Login form -->
        <div v-if="mode === 'login'" class="p-6">
          <p class="text-sm text-zinc-500 dark:text-zinc-400 mb-5">已激活账号请用学号 + PIN 登录</p>

          <div class="space-y-4">
            <div>
              <label class="flex items-center gap-1.5 text-xs text-zinc-500 dark:text-zinc-400 mb-1.5">
                <Hash class="w-3.5 h-3.5" />
                学号
              </label>
              <input
                v-model="loginNumber"
                placeholder="2521241111"
                inputmode="numeric"
                autocomplete="username"
                class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-lg px-3 py-2.5 text-zinc-900 dark:text-zinc-200 placeholder:text-zinc-300 dark:placeholder:text-zinc-700 focus-ring font-mono-token tracking-wider text-base"
              />
            </div>
            <div>
              <label class="flex items-center gap-1.5 text-xs text-zinc-500 dark:text-zinc-400 mb-1.5">
                <ShieldCheck class="w-3.5 h-3.5" />
                PIN <span class="text-zinc-500 dark:text-zinc-600">(4–6 位数字)</span>
              </label>
              <input
                :value="loginPin"
                @input="(e: any) => (loginPin = pinDigits(e.target.value))"
                placeholder="••••"
                type="password"
                inputmode="numeric"
                autocomplete="current-password"
                maxlength="6"
                class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-lg px-3 py-2.5 text-zinc-900 dark:text-zinc-200 placeholder:text-zinc-300 dark:placeholder:text-zinc-700 focus-ring font-mono-token tracking-[0.4em] text-center text-base"
                @keyup.enter="submit"
              />
            </div>
          </div>

          <button
            @click="submit"
            :disabled="!canSubmit"
            class="mt-5 w-full inline-flex items-center justify-center gap-2 bg-emerald-500 hover:bg-emerald-400 disabled:bg-zinc-200 dark:disabled:bg-zinc-800 disabled:text-zinc-500 text-zinc-950 font-medium py-2.5 rounded-xl transition-all disabled:cursor-not-allowed"
          >
            <span>{{ submitting ? '登录中…' : '登录' }}</span>
            <ArrowRight v-if="!submitting" class="w-4 h-4" />
          </button>

          <div
            v-if="error"
            class="mt-3 flex items-start gap-2 px-3 py-2 rounded-lg bg-red-500/10 ring-1 ring-red-500/20 text-sm text-red-400"
          >
            <AlertCircle class="w-4 h-4 shrink-0 mt-0.5" />
            <span>{{ error }}</span>
          </div>

          <button
            @click="switchTo('activate')"
            class="mt-5 w-full text-xs text-zinc-500 hover:text-emerald-400 transition-colors"
          >
            首次使用？前往激活 →
          </button>
        </div>

        <!-- Activate form -->
        <div v-else class="p-6">
          <p class="text-sm text-zinc-500 dark:text-zinc-400 mb-5">首次使用，用邀请码 + 电脑微信授权完成激活，并设置登录 PIN</p>

          <div class="space-y-4">
            <!-- Step indicator: shown only in activate flow. Random visitors
                 see step 1 only — the wechat-OAuth UI is hidden until the
                 server confirms the invite code is real. -->
            <div class="flex items-center justify-center gap-2 text-[11px]">
              <span
                class="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md transition-colors"
                :class="activateStep === 'credentials'
                  ? 'bg-emerald-500/15 text-emerald-300 ring-1 ring-emerald-500/30'
                  : 'text-zinc-500 dark:text-zinc-600'"
              >
                <span
                  class="w-4 h-4 rounded-full flex items-center justify-center text-[9px] font-bold"
                  :class="activateStep === 'credentials'
                    ? 'bg-emerald-500 text-zinc-950'
                    : 'bg-zinc-300 dark:bg-zinc-700 text-zinc-600 dark:text-zinc-400'"
                >1</span>
                <span>凭证</span>
              </span>
              <span class="text-zinc-400 dark:text-zinc-700">→</span>
              <span
                class="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md transition-colors"
                :class="activateStep === 'token'
                  ? 'bg-emerald-500/15 text-emerald-300 ring-1 ring-emerald-500/30'
                  : 'text-zinc-500 dark:text-zinc-600'"
              >
                <span
                  class="w-4 h-4 rounded-full flex items-center justify-center text-[9px] font-bold"
                  :class="activateStep === 'token'
                    ? 'bg-emerald-500 text-zinc-950'
                    : 'bg-zinc-300 dark:bg-zinc-700 text-zinc-600 dark:text-zinc-400'"
                >2</span>
                <span>学校 Token</span>
              </span>
            </div>

            <!-- ============ Step 1: credentials ============ -->
            <div v-show="activateStep === 'credentials'" class="space-y-4">
              <div>
                <label class="flex items-center gap-1.5 text-xs text-zinc-500 dark:text-zinc-400 mb-1.5">
                  <Ticket class="w-3.5 h-3.5" />
                  邀请码
                </label>
                <input
                  v-model="inviteCode"
                  placeholder="ABC-DEF-XYZ9"
                  autocomplete="off"
                  autocapitalize="characters"
                  class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-lg px-3 py-2.5 font-mono-token text-zinc-900 dark:text-zinc-200 placeholder:text-zinc-300 dark:placeholder:text-zinc-700 focus-ring tracking-wider text-center text-base"
                />
              </div>

              <!-- PIN warning -->
              <div class="rounded-lg bg-amber-500/[0.07] ring-1 ring-amber-500/25 p-3 text-[11px] text-zinc-700 dark:text-zinc-300 leading-relaxed">
                <p>
                  <strong class="text-amber-300">⚠️ PIN 就是你以后登录 wangui 的密码</strong>，
                  4–6 位数字，自己定，<strong>记牢别忘</strong>。
                </p>
                <p class="text-zinc-500 dark:text-zinc-400 mt-1">
                  以后登录用「学号 + 这个 PIN」就行，不需要再扫码。<br />
                  忘了的话只能让管理员给你重置。
                </p>
              </div>
              <div class="grid grid-cols-2 gap-3">
                <div>
                  <label class="flex items-center gap-1.5 text-xs text-zinc-500 dark:text-zinc-400 mb-1.5">
                    <ShieldCheck class="w-3.5 h-3.5" />
                    设置登录 PIN
                  </label>
                  <input
                    :value="pinA"
                    @input="(e: any) => (pinA = pinDigits(e.target.value))"
                    placeholder="4–6 位数字"
                    type="password"
                    inputmode="numeric"
                    autocomplete="new-password"
                    maxlength="6"
                    class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-lg px-3 py-2.5 text-zinc-900 dark:text-zinc-200 placeholder:text-zinc-300 dark:placeholder:text-zinc-700 focus-ring font-mono-token tracking-[0.4em] text-center"
                  />
                </div>
                <div>
                  <label class="flex items-center gap-1.5 text-xs text-zinc-500 dark:text-zinc-400 mb-1.5">
                    <ShieldCheck class="w-3.5 h-3.5" />
                    再输一次
                  </label>
                  <input
                    :value="pinB"
                    @input="(e: any) => (pinB = pinDigits(e.target.value))"
                    placeholder="重复"
                    type="password"
                    inputmode="numeric"
                    maxlength="6"
                    class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-lg px-3 py-2.5 text-zinc-900 dark:text-zinc-200 placeholder:text-zinc-300 dark:placeholder:text-zinc-700 focus-ring font-mono-token tracking-[0.4em] text-center"
                  />
                </div>
              </div>
              <p
                v-if="pinMatchHint"
                class="-mt-2 text-[11px] text-amber-400 inline-flex items-center gap-1"
              >
                <AlertCircle class="w-3 h-3" />
                {{ pinMatchHint }}
              </p>

              <!-- Disclaimer inline -->
              <div class="rounded-lg bg-white/50 dark:bg-zinc-950/50 ring-1 ring-black/[0.05] dark:ring-white/[0.04] p-3">
                <button
                  type="button"
                  @click="toggleDisclaimer"
                  class="w-full flex items-center justify-between text-xs text-zinc-500 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-200 transition-colors"
                >
                  <span class="inline-flex items-center gap-1.5">
                    <AlertCircle class="w-3.5 h-3.5 text-amber-400" />
                    使用须知
                  </span>
                  <span class="text-[10px] text-zinc-500 dark:text-zinc-600">{{ showDisclaimerDetail ? '收起 ↑' : '展开 ↓' }}</span>
                </button>

                <Transition name="expand">
                  <ul v-if="showDisclaimerDetail" class="mt-3 space-y-2.5 overflow-hidden">
                    <li v-for="(it, i) in disclaimerItems" :key="i" class="flex gap-2.5">
                      <component :is="it.icon" class="w-3.5 h-3.5 shrink-0 mt-0.5" :class="it.color" />
                      <p class="text-[11px] text-zinc-500 dark:text-zinc-400 leading-relaxed">{{ it.text }}</p>
                    </li>
                  </ul>
                </Transition>

                <label
                  class="mt-3 flex items-center gap-2 select-none"
                  :class="canCheckAgreement ? 'cursor-pointer' : 'cursor-not-allowed opacity-60'"
                >
                  <input
                    type="checkbox"
                    v-model="agreed"
                    :disabled="!canCheckAgreement"
                    class="sr-only peer"
                  />
                  <div class="w-4 h-4 rounded border-2 border-zinc-600 peer-checked:border-emerald-500 peer-checked:bg-emerald-500 transition-colors flex items-center justify-center shrink-0">
                    <Check v-if="agreed" class="w-2.5 h-2.5 text-zinc-950" :stroke-width="3" />
                  </div>
                  <span class="text-xs text-zinc-700 dark:text-zinc-300">
                    <template v-if="canCheckAgreement">我已阅读并同意</template>
                    <template v-else-if="showDisclaimerDetail">阅读中… ({{ readCountdown }}s)</template>
                    <template v-else>请先展开「使用须知」并阅读完</template>
                  </span>
                </label>
              </div>
            </div>

            <!-- ============ Step 2: school token ============ -->
            <div v-show="activateStep === 'token'" class="space-y-4">
              <button
                type="button"
                @click="backToCredentials"
                class="text-[11px] text-zinc-500 dark:text-zinc-400 hover:text-emerald-400 transition-colors inline-flex items-center gap-1"
              >
                ← 上一步：改邀请码 / PIN
              </button>

              <div>
                <label class="flex items-center gap-1.5 text-xs text-zinc-500 dark:text-zinc-400 mb-1.5">
                  <QrCode class="w-3.5 h-3.5" />
                  电脑微信授权获取学校 Token
                </label>
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
                    <ol class="text-[12px] text-zinc-700 dark:text-zinc-300 space-y-2 list-decimal list-inside leading-relaxed">
                      <li>复制本轮授权链接，发送给自己的<strong>电脑微信</strong></li>
                      <li>在电脑微信中打开链接并完成授权</li>
                      <li>看到学校图片页后，右上「⋯」→「复制链接」</li>
                      <li>把完整链接粘到下方；本轮链接约 5 分钟有效</li>
                    </ol>
                    <div class="mt-3">
                      <button
                        type="button"
                        @click="copyWechatAuthorizationUrl"
                        :disabled="!wechatAuthorizationUrl"
                        class="mr-2 inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[11px] text-zinc-700 dark:text-zinc-300 bg-white/80 dark:bg-zinc-900/80 ring-1 ring-black/[0.06] dark:ring-white/[0.05] hover:ring-emerald-500/40 transition-colors disabled:opacity-50"
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
              <div class="mt-3 rounded-2xl bg-emerald-500/[0.08] ring-1 ring-emerald-500/30 p-4 shadow-[0_10px_30px_rgba(16,185,129,0.08)]">
                <div class="flex items-start justify-between gap-3">
                  <div>
                    <label class="flex items-center gap-1.5 text-sm font-semibold text-zinc-900 dark:text-zinc-100">
                      <KeyRound class="w-4 h-4 text-emerald-400" />
                      把电脑微信复制的完整回调链接贴这里
                    </label>
                    <p class="mt-1 text-[11px] text-zinc-600 dark:text-zinc-400 leading-relaxed">
                      必须是学校图片页的完整 HTTPS URL，并同时包含本轮唯一的 code 和 state；纯 code 无法保留本轮校验信息。
                    </p>
                  </div>
                </div>
                <textarea
                  v-model="callbackUrl"
                  placeholder="https://xhbcs.henau.edu.cn/农大白.png?code=EXAMPLE&state=EXAMPLE"
                  class="mt-3 w-full bg-white dark:bg-zinc-950 ring-2 ring-emerald-500/25 focus:!ring-emerald-500/55 rounded-xl px-3 py-3 h-32 resize-none text-zinc-900 dark:text-zinc-200 placeholder:text-zinc-400 dark:placeholder:text-zinc-600 focus-ring"
                />
                <div
                  v-if="callbackDetection.kind === 'callback-url'"
                  class="mt-2 inline-flex items-center gap-1.5 rounded-lg bg-emerald-500/12 px-2.5 py-1.5 text-[11px] text-emerald-500"
                >
                  <Check class="w-3.5 h-3.5" />
                  已识别学校图片回调，将由服务端核对本轮 state
                </div>
                <div
                  v-else-if="callbackDetection.kind === 'invalid' && callbackUrl.trim()"
                  class="mt-2 inline-flex items-center gap-1.5 rounded-lg bg-amber-500/12 px-2.5 py-1.5 text-[11px] text-amber-400"
                >
                  <AlertCircle class="w-3.5 h-3.5" />
                  回调格式不正确，请粘贴电脑微信复制的完整学校图片 URL。
                </div>
              </div>

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
                    v-model="token"
                    placeholder="eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."
                    class="w-full bg-white dark:bg-zinc-950 ring-1 ring-black/[0.08] dark:ring-white/[0.06] rounded-lg px-3 py-2.5 font-mono-token h-24 resize-none text-zinc-900 dark:text-zinc-200 placeholder:text-zinc-300 dark:placeholder:text-zinc-700 focus-ring"
                  />
                </div>
              </Transition>
            </div>

            </div>
            <!-- /step 2 -->
          </div>

          <button
            @click="submit"
            :disabled="!canSubmit"
            class="mt-5 w-full inline-flex items-center justify-center gap-2 bg-emerald-500 hover:bg-emerald-400 disabled:bg-zinc-200 dark:disabled:bg-zinc-800 disabled:text-zinc-500 text-zinc-950 font-medium py-2.5 rounded-xl transition-all disabled:cursor-not-allowed"
          >
            <span v-if="submitting">激活中…</span>
            <span v-else-if="activateStep === 'credentials' && !precheckLoading">下一步：获取学校 Token</span>
            <span v-else-if="precheckLoading">校验邀请码…</span>
            <span v-else>激活账号</span>
            <ArrowRight v-if="!submitting && !precheckLoading" class="w-4 h-4" />
          </button>

          <div
            v-if="error"
            class="mt-3 flex items-start gap-2 px-3 py-2 rounded-lg bg-red-500/10 ring-1 ring-red-500/20 text-sm text-red-400"
          >
            <AlertCircle class="w-4 h-4 shrink-0 mt-0.5" />
            <span>{{ error }}</span>
          </div>

          <button
            @click="switchTo('login')"
            class="mt-5 w-full text-xs text-zinc-500 hover:text-emerald-400 transition-colors"
          >
            ← 已激活账号？返回登录
          </button>
        </div>
      </div>

      <p class="text-center text-[11px] text-zinc-500 dark:text-zinc-600 mt-5">
        登录限速 5 次 / 分钟 · Token 加密存储 · PIN bcrypt 哈希
      </p>
    </div>
  </div>
</template>

<style scoped>
.expand-enter-active, .expand-leave-active { transition: all 0.25s ease; max-height: 260px; }
.expand-enter-from, .expand-leave-to { max-height: 0; opacity: 0; overflow: hidden; }
</style>
