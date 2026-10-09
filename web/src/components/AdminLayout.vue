<script setup lang="ts">
import { onMounted } from 'vue'
import { useRouter } from 'vue-router'
import {
  LayoutDashboard,
  Ticket,
  Building2,
  Users,
  UserPlus,
  ScrollText,
  Cog,
  LogOut,
  ShieldCheck,
  Activity,
  Megaphone,
} from 'lucide-vue-next'
import Logo from './Logo.vue'
import ThemeToggle from './ThemeToggle.vue'
import SidebarNav, { type NavItem } from './SidebarNav.vue'
import { useAdminAuth } from '../stores/auth'
import { adminApi } from '../api'
import { showToast } from '../lib/toast'

const router = useRouter()
const admin = useAdminAuth()

const items: NavItem[] = [
  { to: '/rosekhlifa', label: '概览', icon: LayoutDashboard },
  { to: '/rosekhlifa/monitor', label: '监控看板', icon: Activity },
  { to: '/rosekhlifa/announcements', label: '公告', icon: Megaphone },
  { to: '/rosekhlifa/codes', label: '邀请码', icon: Ticket },
  { to: '/rosekhlifa/dorms', label: '宿舍楼', icon: Building2 },
  { to: '/rosekhlifa/users', label: '用户', icon: Users },
  { to: '/rosekhlifa/guests', label: '临时朋友', icon: UserPlus },
  { to: '/rosekhlifa/logs', label: '日志', icon: ScrollText },
  { to: '/rosekhlifa/settings', label: '设置', icon: Cog },
]

async function logout() {
  try {
    await adminApi.logout()
  } finally {
    admin.setAdmin(false)
    showToast('ok', '已退出管理员')
    router.push('/rosekhlifa/login')
  }
}

onMounted(() => {
  if (!admin.state.initialized) admin.init()
})
</script>

<template>
  <div class="relative flex flex-col md:flex-row mx-auto max-w-[1700px] bg-white dark:bg-zinc-950 min-h-screen">
    <aside class="hidden md:flex flex-col w-64 shrink-0 sticky top-0 h-screen border-r border-amber-500/10 bg-white/60 dark:bg-zinc-950/60 backdrop-blur-xl overflow-y-auto">
      <div class="px-5 py-5 border-b border-black/[0.05] dark:border-white/[0.04] flex items-center justify-between gap-2">
        <Logo :size="34" text="晚归管理端" subtitle="仅供运维" />
        <span class="inline-flex items-center gap-1 px-2 py-0.5 rounded-md text-[10px] font-medium bg-amber-500/15 text-amber-400 ring-1 ring-amber-500/30 shrink-0">
          <ShieldCheck class="w-2.5 h-2.5" />
          ADMIN
        </span>
      </div>

      <div class="flex-1 px-3 py-5">
        <p class="px-3 mb-2 text-[10px] uppercase tracking-wider text-zinc-500 dark:text-zinc-600 font-medium">管理</p>
        <SidebarNav :items="items" />
      </div>

      <div class="px-3 py-3 border-t border-black/[0.05] dark:border-white/[0.04] flex items-center gap-1">
        <button
          @click="logout"
          class="flex-1 flex items-center gap-2.5 px-3 py-2 rounded-lg text-sm text-zinc-500 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-100 hover:bg-black/5 dark:hover:bg-white/5 transition-colors"
        >
          <LogOut class="w-4 h-4 text-zinc-500" />
          <span>退出管理员</span>
        </button>
        <ThemeToggle />
      </div>
    </aside>

    <header class="md:hidden sticky top-0 z-30 bg-white/85 dark:bg-zinc-950/85 backdrop-blur-xl border-b border-black/[0.08] dark:border-white/[0.06] h-12 flex items-center justify-between px-4">
      <Logo :size="26" text="晚归管理端" />
      <div class="flex items-center gap-2">
        <ThemeToggle />
        <span class="inline-flex items-center gap-1 px-2 py-0.5 rounded-md text-[10px] font-medium bg-amber-500/15 text-amber-400 ring-1 ring-amber-500/30">
          <ShieldCheck class="w-2.5 h-2.5" />
          ADMIN
        </span>
      </div>
    </header>

    <main class="relative flex-1 min-w-0">
      <div class="px-3 sm:px-6 md:px-10 lg:px-14 py-4 sm:py-6 md:py-8 pb-24 md:pb-16">
        <RouterView v-slot="{ Component }">
          <Transition name="fade" mode="out-in">
            <component :is="Component" />
          </Transition>
        </RouterView>
      </div>

      <nav class="md:hidden fixed bottom-0 inset-x-0 z-30 bg-white/90 dark:bg-zinc-950/90 backdrop-blur-xl border-t border-black/[0.08] dark:border-white/[0.06] flex justify-around py-2">
        <RouterLink
          v-for="item in items"
          :key="item.to"
          :to="item.to"
          v-slot="{ isExactActive }"
          custom
        >
          <a
            :href="item.to"
            @click.prevent="$router.push(item.to)"
            class="flex flex-col items-center gap-0.5 px-2 py-1.5 rounded-lg"
            :class="isExactActive ? 'text-amber-400' : 'text-zinc-500'"
          >
            <component :is="item.icon" class="w-5 h-5" />
            <span class="text-[10px]">{{ item.label }}</span>
          </a>
        </RouterLink>
      </nav>
    </main>
  </div>
</template>

<style scoped>
.fade-enter-active, .fade-leave-active { transition: opacity 0.15s ease, transform 0.15s ease; }
.fade-enter-from, .fade-leave-to { opacity: 0; transform: translateY(4px); }
</style>
