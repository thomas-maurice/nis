<template>
  <nav class="navbar navbar-expand-lg navbar-dark bg-primary">
    <div class="container-fluid">
      <router-link class="navbar-brand" to="/">
        <font-awesome-icon :icon="['fas', 'server']" class="me-2" />
        NATS Identity Service
      </router-link>

      <button
        class="navbar-toggler"
        type="button"
        data-bs-toggle="collapse"
        data-bs-target="#navbarNav"
      >
        <span class="navbar-toggler-icon"></span>
      </button>

      <div class="collapse navbar-collapse" id="navbarNav">
        <ul class="navbar-nav me-auto">
          <li class="nav-item">
            <router-link class="nav-link" to="/">
              <font-awesome-icon :icon="['fas', 'home']" class="me-1" />
              Dashboard
            </router-link>
          </li>
          <li v-if="authStore.isAdmin || authStore.isOperatorAdmin" class="nav-item">
            <router-link class="nav-link" to="/operators">
              <font-awesome-icon :icon="['fas', 'server']" class="me-1" />
              Operators
            </router-link>
          </li>
          <li class="nav-item">
            <router-link class="nav-link" to="/accounts">
              <font-awesome-icon :icon="['fas', 'users']" class="me-1" />
              Accounts
            </router-link>
          </li>
          <li class="nav-item">
            <router-link class="nav-link" to="/users">
              <font-awesome-icon :icon="['fas', 'user']" class="me-1" />
              Users
            </router-link>
          </li>
          <li v-if="authStore.isAdmin || authStore.isOperatorAdmin" class="nav-item">
            <router-link class="nav-link" to="/signing-keys">
              <font-awesome-icon :icon="['fas', 'key']" class="me-1" />
              Signing Keys
            </router-link>
          </li>
          <li v-if="authStore.isAdmin || authStore.isOperatorAdmin" class="nav-item">
            <router-link class="nav-link" to="/clusters">
              <font-awesome-icon :icon="['fas', 'network-wired']" class="me-1" />
              Clusters
            </router-link>
          </li>
          <li v-if="authStore.isAdmin" class="nav-item">
            <router-link class="nav-link" to="/api-users">
              <font-awesome-icon :icon="['fas', 'user-shield']" class="me-1" />
              API Users
            </router-link>
          </li>
        </ul>

        <ul class="navbar-nav">
          <li class="nav-item dropdown">
            <a
              class="nav-link dropdown-toggle"
              href="#"
              id="navbarDropdown"
              role="button"
              data-bs-toggle="dropdown"
            >
              <font-awesome-icon :icon="['fas', 'user']" class="me-1" />
              {{ authStore.username || 'User' }}
            </a>
            <ul class="dropdown-menu dropdown-menu-end">
              <li>
                <a class="dropdown-item" href="#" @click.prevent="logout">
                  <font-awesome-icon :icon="['fas', 'sign-out-alt']" class="me-2" />
                  Logout
                </a>
              </li>
            </ul>
          </li>
        </ul>
      </div>
    </div>
  </nav>
</template>

<script setup>
import { useAuthStore } from '@/stores/auth'
import { useRouter } from 'vue-router'

const authStore = useAuthStore()
const router = useRouter()

const logout = () => {
  authStore.logout()
  router.push('/login')
}
</script>

<style scoped>
.navbar-brand {
  font-weight: 600;
  font-size: 1.3rem;
}

.nav-link {
  font-weight: 500;
  transition: opacity 0.2s;
}

.nav-link:hover {
  opacity: 0.8;
}

.router-link-active {
  font-weight: 600;
}
</style>
