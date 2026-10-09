import { createRouter, createWebHistory, RouteRecordRaw } from 'vue-router'
import { useAuth, useAdminAuth } from './stores/auth'

const routes: RouteRecordRaw[] = [
  // Public
  {
    path: '/login',
    name: 'login',
    component: () => import('./views/Login.vue'),
    meta: { layout: 'none' },
  },
  {
    path: '/rosekhlifa/login',
    name: 'admin-login',
    component: () => import('./views/admin/Login.vue'),
    meta: { layout: 'none' },
  },

  // User area
  {
    path: '/',
    component: () => import('./components/UserLayout.vue'),
    meta: { requiresAuth: true },
    children: [
      { path: '', name: 'dashboard', component: () => import('./views/Dashboard.vue') },
      { path: 'settings', name: 'settings', component: () => import('./views/Settings.vue') },
      { path: 'records', name: 'records', component: () => import('./views/Records.vue') },
      { path: 'account', name: 'account', component: () => import('./views/Account.vue') },
    ],
  },

  // Admin area
  {
    path: '/rosekhlifa',
    component: () => import('./components/AdminLayout.vue'),
    meta: { requiresAdmin: true },
    children: [
      { path: '', name: 'admin-dashboard', component: () => import('./views/admin/Dashboard.vue') },
      { path: 'monitor', name: 'admin-monitor', component: () => import('./views/admin/Monitor.vue') },
      { path: 'announcements', name: 'admin-announcements', component: () => import('./views/admin/Announcements.vue') },
      // Backward compat: /tonight kept as a redirect so any bookmarks still work.
      { path: 'tonight', redirect: '/rosekhlifa/monitor' },
      { path: 'codes', name: 'admin-codes', component: () => import('./views/admin/Codes.vue') },
      { path: 'dorms', name: 'admin-dorms', component: () => import('./views/admin/Dorms.vue') },
      { path: 'users', name: 'admin-users', component: () => import('./views/admin/Users.vue') },
      { path: 'guests', name: 'admin-guests', component: () => import('./views/admin/Guests.vue') },
      { path: 'logs', name: 'admin-logs', component: () => import('./views/admin/Logs.vue') },
      { path: 'settings', name: 'admin-settings', component: () => import('./views/admin/Settings.vue') },
    ],
  },

  // Catch-all
  { path: '/:catchAll(.*)', redirect: '/' },
]

export const router = createRouter({
  history: createWebHistory(),
  routes,
})

router.beforeEach(async (to) => {
  if (to.meta.requiresAuth) {
    const auth = useAuth()
    await auth.init()
    if (!auth.state.me) {
      return { name: 'login', query: { redirect: to.fullPath } }
    }
  }
  if (to.meta.requiresAdmin) {
    const admin = useAdminAuth()
    if (!admin.state.initialized) await admin.init()
    if (!admin.state.isAdmin) {
      return { name: 'admin-login', query: { redirect: to.fullPath } }
    }
  }
})
