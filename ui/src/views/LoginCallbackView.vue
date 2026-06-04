<template>
  <div class="callback-container d-flex justify-content-center align-items-center min-vh-100">
    <div v-if="processing" class="text-center">
      <div class="spinner-border text-primary mb-3" role="status"></div>
      <p class="text-muted">Completing sign-in...</p>
    </div>
    <div v-else class="card shadow-lg" style="width: 100%; max-width: 400px;">
      <div class="card-body p-5 text-center">
        <font-awesome-icon :icon="['fas', 'circle-exclamation']" size="3x" class="text-danger mb-3" />
        <h4 class="card-title">SSO login failed</h4>
        <p class="text-muted">{{ errorMessage }}</p>
        <router-link to="/login" class="btn btn-primary mt-2">Back to Login</router-link>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const router = useRouter()
const authStore = useAuthStore()
const processing = ref(true)
const errorMessage = ref('No token was received from the identity provider.')

onMounted(() => {
  const hash = window.location.hash.startsWith('#') ? window.location.hash.slice(1) : window.location.hash
  const params = new URLSearchParams(hash)
  const token = params.get('token')
  const redirect = params.get('redirect') || '/'
  const err = params.get('error')
  const errDesc = params.get('error_description')

  if (token) {
    authStore.login(token)
    history.replaceState(null, '', window.location.pathname)
    router.replace(redirect)
  } else {
    if (err) {
      errorMessage.value = errDesc ? `${err}: ${errDesc}` : err
    }
    history.replaceState(null, '', window.location.pathname)
    processing.value = false
  }
})
</script>

<style scoped>
.callback-container {
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
}
</style>
