<template>
  <div class="container-fluid py-4">
    <div v-if="loading" class="text-center py-5">
      <div class="spinner-border text-primary" role="status"></div>
    </div>

    <div v-else-if="error" class="alert alert-danger">{{ error }}</div>

    <div v-else-if="cluster">
      <div class="d-flex justify-content-between align-items-center mb-4">
        <h1>{{ cluster.name }}</h1>
        <div>
          <button class="btn btn-primary me-2" @click="syncCluster" :disabled="syncing">
            <span v-if="syncing" class="spinner-border spinner-border-sm me-2"></span>
            <font-awesome-icon v-else :icon="['fas', 'sync']" class="me-2" />
            Sync Accounts
          </button>
          <router-link to="/clusters" class="btn btn-outline-secondary">
            Back to Clusters
          </router-link>
        </div>
      </div>

      <div v-if="syncSuccess" class="alert alert-success alert-dismissible fade show" role="alert">
        {{ syncSuccess }}
        <button type="button" class="btn-close" @click="syncSuccess = ''"></button>
      </div>

      <div v-if="syncError" class="alert alert-danger alert-dismissible fade show" role="alert">
        {{ syncError }}
        <button type="button" class="btn-close" @click="syncError = ''"></button>
      </div>

      <div class="row g-4">
        <div class="col-md-6">
          <div class="card">
            <div class="card-header">
              <h5 class="mb-0">Cluster Details</h5>
            </div>
            <div class="card-body">
              <dl class="row mb-0">
                <dt class="col-sm-4">ID:</dt>
                <dd class="col-sm-8"><code>{{ cluster.id }}</code></dd>

                <dt class="col-sm-4">Name:</dt>
                <dd class="col-sm-8">{{ cluster.name }}</dd>

                <dt class="col-sm-4">Description:</dt>
                <dd class="col-sm-8">{{ cluster.description || '-' }}</dd>

                <dt class="col-sm-4">Server URLs:</dt>
                <dd class="col-sm-8">
                  <ul class="list-unstyled mb-0">
                    <li v-for="(url, index) in cluster.serverUrls" :key="index">
                      <code>{{ url }}</code>
                    </li>
                  </ul>
                </dd>

                <dt class="col-sm-4">System Account:</dt>
                <dd class="col-sm-8">
                  <code v-if="cluster.systemAccountPubKey">{{ cluster.systemAccountPubKey }}</code>
                  <span v-else class="text-muted">Not set</span>
                </dd>

                <dt class="col-sm-4">Created:</dt>
                <dd class="col-sm-8">{{ formatDate(cluster.createdAt) }}</dd>
              </dl>
            </div>
          </div>
        </div>

        <div class="col-md-6">
          <div class="card">
            <div class="card-header">
              <h5 class="mb-0">Sync Information</h5>
            </div>
            <div class="card-body">
              <p class="card-text">
                Use the "Sync Accounts" button to push all accounts from this operator to the NATS cluster.
                This will update the JWT resolver with the latest account JWTs.
              </p>
              <div class="alert alert-info mb-0">
                <strong>Note:</strong> The cluster must be configured with the operator JWT and have
                the $SYS account credentials configured for sync to work.
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import apiClient from '@/utils/api'

const route = useRoute()
const cluster = ref(null)
const loading = ref(false)
const error = ref('')
const syncing = ref(false)
const syncSuccess = ref('')
const syncError = ref('')

const loadCluster = async () => {
  loading.value = true
  error.value = ''
  try {
    const response = await apiClient.post('/nis.v1.ClusterService/GetCluster', {
      id: route.params.id
    })
    cluster.value = response.data.cluster
  } catch (err) {
    error.value = err.response?.data?.message || 'Failed to load cluster'
  } finally {
    loading.value = false
  }
}

const syncCluster = async () => {
  syncing.value = true
  syncSuccess.value = ''
  syncError.value = ''

  try {
    const response = await apiClient.post('/nis.v1.ClusterService/SyncCluster', {
      id: cluster.value.id
    })
    syncSuccess.value = `Successfully synced ${response.data.accountCount || 0} account(s) to cluster`
  } catch (err) {
    syncError.value = err.response?.data?.message || 'Failed to sync cluster'
  } finally {
    syncing.value = false
  }
}

const formatDate = (dateStr) => {
  if (!dateStr) return '-'
  return new Date(dateStr).toLocaleString()
}

onMounted(() => {
  loadCluster()
})
</script>

<style scoped>
dt {
  font-weight: 600;
}

dd {
  margin-bottom: 0.5rem;
}

ul {
  padding-left: 0;
}
</style>
