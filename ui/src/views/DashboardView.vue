<template>
  <div class="container-fluid py-4">
    <h1 class="mb-4">Dashboard</h1>

    <div class="row g-4">
      <div class="col-md-3">
        <div class="card text-center">
          <div class="card-body">
            <font-awesome-icon :icon="['fas', 'server']" size="3x" class="text-primary mb-3" />
            <h3 class="card-title">{{ stats.operators || 0 }}</h3>
            <p class="card-text text-muted">Operators</p>
            <router-link to="/operators" class="btn btn-sm btn-outline-primary">View</router-link>
          </div>
        </div>
      </div>

      <div class="col-md-3">
        <div class="card text-center">
          <div class="card-body">
            <font-awesome-icon :icon="['fas', 'users']" size="3x" class="text-success mb-3" />
            <h3 class="card-title">{{ stats.accounts || 0 }}</h3>
            <p class="card-text text-muted">Accounts</p>
            <router-link to="/accounts" class="btn btn-sm btn-outline-success">View</router-link>
          </div>
        </div>
      </div>

      <div class="col-md-3">
        <div class="card text-center">
          <div class="card-body">
            <font-awesome-icon :icon="['fas', 'user']" size="3x" class="text-info mb-3" />
            <h3 class="card-title">{{ stats.users || 0 }}</h3>
            <p class="card-text text-muted">Users</p>
            <router-link to="/users" class="btn btn-sm btn-outline-info">View</router-link>
          </div>
        </div>
      </div>

      <div class="col-md-3">
        <div class="card text-center">
          <div class="card-body">
            <font-awesome-icon :icon="['fas', 'network-wired']" size="3x" class="text-warning mb-3" />
            <h3 class="card-title">{{ stats.clusters || 0 }}</h3>
            <p class="card-text text-muted">Clusters</p>
            <router-link to="/clusters" class="btn btn-sm btn-outline-warning">View</router-link>
          </div>
        </div>
      </div>
    </div>

    <div class="row mt-4">
      <div class="col-12">
        <div class="card">
          <div class="card-body">
            <h5 class="card-title">Welcome to NATS Identity Service</h5>
            <p class="card-text">
              Manage your NATS JWT authentication infrastructure with ease. Create operators,
              accounts, users, and sync them to your NATS clusters.
            </p>
            <div class="mt-3">
              <router-link to="/operators" class="btn btn-primary me-2">Get Started</router-link>
              <a
                href="https://docs.nats.io/running-a-nats-service/configuration/securing_nats/auth_intro/jwt"
                target="_blank"
                class="btn btn-outline-secondary"
              >
                Documentation
              </a>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import apiClient from '@/utils/api'

const stats = ref({
  operators: 0,
  accounts: 0,
  users: 0,
  clusters: 0
})

const loadStats = async () => {
  try {
    // Make parallel requests to get counts
    const [operators, accounts, users, clusters] = await Promise.allSettled([
      apiClient.post('/nis.v1.OperatorService/ListOperators', {}),
      apiClient.post('/nis.v1.AccountService/ListAccounts', {}),
      apiClient.post('/nis.v1.UserService/ListUsers', {}),
      apiClient.post('/nis.v1.ClusterService/ListClusters', {})
    ])

    if (operators.status === 'fulfilled') {
      stats.value.operators = operators.value.data.operators?.length || 0
    }
    if (accounts.status === 'fulfilled') {
      stats.value.accounts = accounts.value.data.accounts?.length || 0
    }
    if (users.status === 'fulfilled') {
      stats.value.users = users.value.data.users?.length || 0
    }
    if (clusters.status === 'fulfilled') {
      stats.value.clusters = clusters.value.data.clusters?.length || 0
    }
  } catch (error) {
    console.error('Failed to load stats:', error)
  }
}

onMounted(() => {
  loadStats()
})
</script>

<style scoped>
h1 {
  font-weight: 600;
  color: #212529;
}

.card {
  transition: transform 0.2s, box-shadow 0.2s;
}

.card:hover {
  transform: translateY(-2px);
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15);
}

.card-title {
  font-weight: 600;
}
</style>
