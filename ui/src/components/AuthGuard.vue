<template>
  <div>
    <slot v-if="isAuthenticated"></slot>
    <div v-else class="d-flex justify-content-center align-items-center min-vh-100">
      <div class="spinner-border text-primary" role="status">
        <span class="visually-hidden">Loading...</span>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { useRouter } from 'vue-router'
import apiClient from '@/utils/api'

const authStore = useAuthStore()
const router = useRouter()
const isAuthenticated = ref(authStore.checkAuth())
let intervalId = null

const checkSession = async () => {
  if (!authStore.checkAuth()) {
    isAuthenticated.value = false
    router.push('/login')
    return
  }

  try {
    // Ping the API to validate session
    await apiClient.get('/api/health')
    isAuthenticated.value = true
  } catch (error) {
    if (error.response?.status === 401) {
      authStore.logout()
      isAuthenticated.value = false
      router.push('/login')
    }
  }
}

onMounted(() => {
  checkSession()
  // Check session every 30 seconds
  intervalId = setInterval(checkSession, 30000)
})

onUnmounted(() => {
  if (intervalId) {
    clearInterval(intervalId)
  }
})
</script>
