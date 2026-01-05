<template>
  <div class="login-container d-flex justify-content-center align-items-center min-vh-100">
    <div class="card shadow-lg" style="width: 100%; max-width: 400px;">
      <div class="card-body p-5">
        <div class="text-center mb-4">
          <font-awesome-icon :icon="['fas', 'server']" size="3x" class="text-primary mb-3" />
          <h3 class="card-title">NATS Identity Service</h3>
          <p class="text-muted">Sign in to continue</p>
        </div>

        <div v-if="error" class="alert alert-danger" role="alert">
          {{ error }}
        </div>

        <form @submit.prevent="handleLogin">
          <div class="mb-3">
            <label for="username" class="form-label">Username</label>
            <input
              id="username"
              v-model="username"
              type="text"
              class="form-control"
              placeholder="Enter username"
              required
              :disabled="loading"
            />
          </div>

          <div class="mb-4">
            <label for="password" class="form-label">Password</label>
            <input
              id="password"
              v-model="password"
              type="password"
              class="form-control"
              placeholder="Enter password"
              required
              :disabled="loading"
            />
          </div>

          <button
            type="submit"
            class="btn btn-primary w-100"
            :disabled="loading"
          >
            <span v-if="loading" class="spinner-border spinner-border-sm me-2"></span>
            {{ loading ? 'Signing in...' : 'Sign In' }}
          </button>
        </form>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import apiClient from '@/utils/api'

const router = useRouter()
const route = useRoute()
const authStore = useAuthStore()

const username = ref('')
const password = ref('')
const loading = ref(false)
const error = ref('')

const handleLogin = async () => {
  error.value = ''
  loading.value = true

  try {
    // Call Connect-RPC authentication endpoint
    const response = await apiClient.post('/nis.v1.AuthService/Login', {
      username: username.value,
      password: password.value
    })

    // Extract token from Connect-RPC response
    const token = response.data.token

    if (token) {
      authStore.login(token)

      // Redirect to original destination or home
      const redirect = route.query.redirect || '/'
      router.push(redirect)
    } else {
      error.value = 'Invalid response from server'
    }
  } catch (err) {
    console.error('Login error:', err)
    error.value = err.response?.data?.message || 'Invalid username or password'
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.login-container {
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
}

.card {
  border-radius: 15px;
}

.form-control:focus {
  border-color: #667eea;
  box-shadow: 0 0 0 0.2rem rgba(102, 126, 234, 0.25);
}

.btn-primary {
  background-color: #667eea;
  border-color: #667eea;
  font-weight: 600;
  padding: 0.75rem;
}

.btn-primary:hover {
  background-color: #5568d3;
  border-color: #5568d3;
}
</style>
