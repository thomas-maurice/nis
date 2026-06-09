<template>
  <nav class="navbar navbar-expand-lg navbar-dark bg-primary">
    <div class="container-fluid">
      <router-link class="navbar-brand" to="/">
        <font-awesome-icon :icon="['fas', 'server']" class="me-2" />
        NIS
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
        <div class="me-3 d-none d-lg-block">
          <GlobalSearch />
        </div>

        <ul class="navbar-nav me-auto">
          <li class="nav-item">
            <router-link class="nav-link" to="/">
              <font-awesome-icon :icon="['fas', 'home']" class="me-1" />
              Dashboard
            </router-link>
          </li>

          <!-- Identity: the core CRUD surfaces. Account-admins only need the
               Accounts / Users links; operator-admin and admin see the full set. -->
          <li class="nav-item dropdown">
            <a
              class="nav-link dropdown-toggle"
              href="#"
              id="identityDropdown"
              role="button"
              data-bs-toggle="dropdown"
            >
              <font-awesome-icon :icon="['fas', 'users']" class="me-1" />
              Identity
            </a>
            <ul class="dropdown-menu" aria-labelledby="identityDropdown">
              <li v-if="authStore.isAdmin || authStore.isOperatorAdmin || authStore.isOrgAdmin">
                <router-link class="dropdown-item" to="/operators">
                  <font-awesome-icon :icon="['fas', 'server']" class="me-2" />
                  Operators
                </router-link>
              </li>
              <li>
                <router-link class="dropdown-item" to="/accounts">
                  <font-awesome-icon :icon="['fas', 'users']" class="me-2" />
                  Accounts
                </router-link>
              </li>
              <li>
                <router-link class="dropdown-item" to="/users">
                  <font-awesome-icon :icon="['fas', 'user']" class="me-2" />
                  Users
                </router-link>
              </li>
              <li v-if="authStore.isAdmin || authStore.isOperatorAdmin">
                <router-link class="dropdown-item" to="/signing-keys">
                  <font-awesome-icon :icon="['fas', 'key']" class="me-2" />
                  Signing Keys
                </router-link>
              </li>
              <li v-if="authStore.isAdmin || authStore.isOperatorAdmin">
                <router-link class="dropdown-item" to="/templates">
                  <font-awesome-icon :icon="['fas', 'layer-group']" class="me-2" />
                  Templates
                </router-link>
              </li>
            </ul>
          </li>

          <!-- Organizations: platform-admin only -->
          <li v-if="authStore.isAdmin" class="nav-item">
            <router-link class="nav-link" to="/organizations">
              <font-awesome-icon :icon="['fas', 'building']" class="me-1" />
              Organizations
            </router-link>
          </li>

          <!-- My Organization: org-admin link to own org -->
          <li v-if="authStore.isOrgAdmin && authStore.organizationId" class="nav-item">
            <router-link class="nav-link" :to="'/organizations/' + authStore.organizationId">
              <font-awesome-icon :icon="['fas', 'building']" class="me-1" />
              My Organization
            </router-link>
          </li>

          <!-- Operations: cluster + observability surfaces. Hidden entirely for
               account-admin, since none of these items are visible to that role. -->
          <li v-if="authStore.isAdmin || authStore.isOperatorAdmin || authStore.isOrgAdmin" class="nav-item dropdown">
            <a
              class="nav-link dropdown-toggle"
              href="#"
              id="operationsDropdown"
              role="button"
              data-bs-toggle="dropdown"
            >
              <font-awesome-icon :icon="['fas', 'network-wired']" class="me-1" />
              Operations
            </a>
            <ul class="dropdown-menu" aria-labelledby="operationsDropdown">
              <li>
                <router-link class="dropdown-item" to="/clusters">
                  <font-awesome-icon :icon="['fas', 'network-wired']" class="me-2" />
                  Clusters
                </router-link>
              </li>
              <li>
                <router-link class="dropdown-item" to="/webhooks">
                  <font-awesome-icon :icon="['fas', 'tower-broadcast']" class="me-2" />
                  Webhooks
                </router-link>
              </li>
              <li v-if="authStore.isAdmin">
                <router-link class="dropdown-item" to="/events">
                  <font-awesome-icon :icon="['fas', 'list-alt']" class="me-2" />
                  Events
                </router-link>
              </li>
              <li v-if="authStore.isAdmin">
                <router-link class="dropdown-item" to="/jobs">
                  <font-awesome-icon :icon="['fas', 'clock']" class="me-2" />
                  Background Jobs
                </router-link>
              </li>
              <li v-if="authStore.isAdmin">
                <router-link class="dropdown-item" to="/config">
                  <font-awesome-icon :icon="['fas', 'gears']" class="me-2" />
                  Runtime Config
                </router-link>
              </li>
            </ul>
          </li>

          <li class="nav-item">
            <router-link class="nav-link" to="/docs">
              <font-awesome-icon :icon="['fas', 'book']" class="me-1" />
              Docs
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
                <router-link class="dropdown-item" to="/api-tokens">
                  <font-awesome-icon :icon="['fas', 'key']" class="me-2" />
                  API Tokens
                </router-link>
              </li>
              <li v-if="authStore.isAdmin"><hr class="dropdown-divider" /></li>
              <li v-if="authStore.isAdmin">
                <router-link class="dropdown-item" to="/api-users">
                  <font-awesome-icon :icon="['fas', 'user-shield']" class="me-2" />
                  Local Users
                </router-link>
              </li>
              <li><hr class="dropdown-divider" /></li>
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
import GlobalSearch from '@/components/GlobalSearch.vue'

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

/* Highlight the parent dropdown when one of its children matches the route.
   Bootstrap doesn't propagate router-link-active up to the dropdown toggle, so
   we mirror it via a small CSS rule that catches the active state on items
   inside the menu. */
.dropdown:has(.router-link-active) > .nav-link {
  font-weight: 600;
}
</style>
