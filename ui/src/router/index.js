import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    {
      path: '/login',
      name: 'login',
      component: () => import('@/views/LoginView.vue'),
      meta: { requiresAuth: false }
    },
    {
      path: '/',
      name: 'home',
      component: () => import('@/views/DashboardView.vue'),
      meta: { requiresAuth: true }
    },
    {
      path: '/operators',
      name: 'operators',
      component: () => import('@/views/OperatorsView.vue'),
      meta: { requiresAuth: true }
    },
    {
      path: '/operators/:id',
      name: 'operator-detail',
      component: () => import('@/views/OperatorDetailView.vue'),
      meta: { requiresAuth: true }
    },
    {
      path: '/accounts',
      name: 'accounts',
      component: () => import('@/views/AccountsView.vue'),
      meta: { requiresAuth: true }
    },
    {
      path: '/accounts/:id',
      name: 'account-detail',
      component: () => import('@/views/AccountDetailView.vue'),
      meta: { requiresAuth: true }
    },
    {
      path: '/users',
      name: 'users',
      component: () => import('@/views/UsersView.vue'),
      meta: { requiresAuth: true }
    },
    {
      path: '/users/:id',
      name: 'user-detail',
      component: () => import('@/views/UserDetailView.vue'),
      meta: { requiresAuth: true }
    },
    {
      path: '/clusters',
      name: 'clusters',
      component: () => import('@/views/ClustersView.vue'),
      meta: { requiresAuth: true }
    },
    {
      path: '/clusters/:id',
      name: 'cluster-detail',
      component: () => import('@/views/ClusterDetailView.vue'),
      meta: { requiresAuth: true }
    },
    {
      path: '/signing-keys',
      name: 'signing-keys',
      component: () => import('@/views/SigningKeysView.vue'),
      meta: { requiresAuth: true }
    }
  ]
})

// Navigation guard for authentication
router.beforeEach((to, from, next) => {
  const authStore = useAuthStore()
  const requiresAuth = to.meta.requiresAuth !== false

  if (requiresAuth && !authStore.checkAuth()) {
    next({ name: 'login', query: { redirect: to.fullPath } })
  } else if (to.name === 'login' && authStore.checkAuth()) {
    next({ name: 'home' })
  } else {
    next()
  }
})

export default router
