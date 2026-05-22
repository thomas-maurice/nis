<template>
  <div class="container-fluid py-4">
    <div v-if="syncSuccess" class="alert alert-success alert-dismissible fade show" role="alert">
      {{ syncSuccess }}
      <button type="button" class="btn-close" @click="syncSuccess = ''"></button>
    </div>

    <div v-if="syncError" class="alert alert-danger alert-dismissible fade show" role="alert" style="white-space: pre-line;">
      {{ syncError }}
      <button type="button" class="btn-close" @click="syncError = ''"></button>
    </div>

    <div class="row g-2 mb-3 align-items-end">
      <div class="col-auto">
        <label for="cluster-operator-filter" class="form-label small mb-1">Operator</label>
        <select id="cluster-operator-filter" v-model="operatorFilter" class="form-select form-select-sm" @change="loadFirstPage">
          <option value="">All operators</option>
          <option v-for="op in operators" :key="op.id" :value="op.id">{{ op.name }}</option>
        </select>
      </div>
      <div class="col-auto">
        <label for="cluster-name-filter" class="form-label small mb-1">Name contains</label>
        <input id="cluster-name-filter" v-model="nameLikeInput" type="text" class="form-control form-control-sm" placeholder="substring..." @input="onNameLikeInput" />
      </div>
    </div>

    <EntityList
      title="Clusters"
      entity-name="Cluster"
      :items="clusters"
      :columns="columns"
      :loading="loading"
      :error="error"
      @create="showCreateModal"
      @edit="showEditModal"
      @delete="handleDelete"
      @select="handleSelect"
    >
      <template #custom-actions="{ item }">
        <button
          class="btn btn-outline-success"
          @click="syncCluster(item)"
          :disabled="syncingClusters[item.id]"
          title="Sync Accounts"
        >
          <span v-if="syncingClusters[item.id]" class="spinner-border spinner-border-sm"></span>
          <font-awesome-icon v-else :icon="['fas', 'sync']" />
        </button>
      </template>

      <template #cell-serverUrls="{ item }">
        <span v-if="item.serverUrls && item.serverUrls.length > 0">
          {{ item.serverUrls[0] }}
          <span v-if="item.serverUrls.length > 1" class="badge bg-secondary ms-1">
            +{{ item.serverUrls.length - 1 }}
          </span>
        </span>
        <span v-else class="text-muted">-</span>
      </template>

      <template #cell-healthy="{ item }">
        <span v-if="item.lastHealthCheck">
          <span :class="item.healthy ? 'badge bg-success' : 'badge bg-danger'">
            <font-awesome-icon :icon="['fas', item.healthy ? 'check-circle' : 'times-circle']" class="me-1" />
            {{ item.healthy ? 'Healthy' : 'Unhealthy' }}
          </span>
          <br>
          <small class="text-muted">{{ formatDate(item.lastHealthCheck) }}</small>
          <div v-if="!item.healthy && item.healthCheckError" class="text-danger small mt-1">
            {{ item.healthCheckError }}
          </div>
        </span>
        <span v-else class="badge bg-secondary">
          <font-awesome-icon :icon="['fas', 'question-circle']" class="me-1" />
          Unknown
        </span>
      </template>

      <template #cell-createdAt="{ item }">
        {{ formatDate(item.createdAt) }}
      </template>
    </EntityList>

    <div class="d-flex align-items-center gap-2 mt-2">
      <button class="btn btn-outline-secondary btn-sm" :disabled="cursorStack.length === 0 || loading" @click="loadFirstPage">First</button>
      <button class="btn btn-outline-secondary btn-sm" :disabled="cursorStack.length === 0 || loading" @click="prevPage">Prev</button>
      <span class="text-muted small">Page {{ cursorStack.length + 1 }}</span>
      <button class="btn btn-outline-secondary btn-sm" :disabled="!nextCursor || loading" @click="nextPage">Next</button>
    </div>

    <EntityForm
      v-if="showModal"
      :title="editingCluster ? 'Edit Cluster' : 'Create Cluster'"
      :submit-label="editingCluster ? 'Update' : 'Create'"
      :initial-data="formData"
      :loading="saving"
      :error="formError"
      @submit="handleSubmit"
      @close="closeModal"
    >
      <template #fields="{ formData: localFormData }">
        <div class="mb-3">
          <label for="operatorId" class="form-label">Operator <span class="text-danger">*</span></label>
          <select
            id="operatorId"
            v-model="localFormData.operatorId"
            class="form-select"
            required
            :disabled="editingCluster"
          >
            <option value="">Select operator...</option>
            <option v-for="op in operators" :key="op.id" :value="op.id">
              {{ op.name }}
            </option>
          </select>
        </div>

        <div class="mb-3">
          <label for="name" class="form-label">Name <span class="text-danger">*</span></label>
          <input
            id="name"
            v-model="localFormData.name"
            type="text"
            class="form-control"
            placeholder="prod-cluster"
            required
            :disabled="editingCluster"
          />
        </div>

        <div class="mb-3">
          <label for="description" class="form-label">Description</label>
          <textarea
            id="description"
            v-model="localFormData.description"
            class="form-control"
            rows="2"
          ></textarea>
        </div>

        <div class="mb-3">
          <label for="serverUrls" class="form-label">Server URLs <span class="text-danger">*</span></label>
          <input
            id="serverUrls"
            :value="(localFormData.serverUrls || []).join(';')"
            @input="localFormData.serverUrls = $event.target.value.split(';')"
            type="text"
            class="form-control"
            placeholder="nats://localhost:4222;nats://localhost:4223"
            required
          />
          <div class="form-text">Separate multiple URLs with semicolons</div>
        </div>

        <div class="mb-3">
          <div class="form-check">
            <input
              id="skipVerifyTls"
              v-model="localFormData.skipVerifyTls"
              type="checkbox"
              class="form-check-input"
            />
            <label for="skipVerifyTls" class="form-check-label">
              Skip TLS Certificate Verification
            </label>
            <div class="form-text">Warning: Only use this for testing with self-signed certificates</div>
          </div>
        </div>
      </template>
    </EntityForm>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import apiClient from '@/utils/api'
import EntityList from '@/components/EntityList.vue'
import EntityForm from '@/components/EntityForm.vue'

const router = useRouter()
const clusters = ref([])
const operators = ref([])
const loading = ref(false)
const error = ref('')
const nextCursor = ref('')
const cursorStack = ref([])
const nameLikeFilter = ref('')
const nameLikeInput = ref('')
const operatorFilter = ref('')
let nameLikeTimer = null
const showModal = ref(false)
const editingCluster = ref(null)
const formData = ref({})
const saving = ref(false)
const formError = ref('')
const syncingClusters = ref({})
const syncSuccess = ref('')
const syncError = ref('')
let refreshInterval = null

const columns = [
  { key: 'name', label: 'Name' },
  { key: 'description', label: 'Description' },
  { key: 'serverUrls', label: 'Server URLs' },
  { key: 'healthy', label: 'Status' },
  { key: 'createdAt', label: 'Created' }
]

const loadPage = async (cursor) => {
  loading.value = true
  error.value = ''
  try {
    const req = {
      nameLike: nameLikeFilter.value,
      page: { limit: 50, cursor: cursor || '' }
    }
    if (operatorFilter.value) {
      req.operatorId = operatorFilter.value
    }
    const response = await apiClient.post('/nis.v1.ClusterService/ListClusters', req)
    clusters.value = response.data.clusters || []
    nextCursor.value = response.data.nextCursor || ''
  } catch (err) {
    error.value = err.response?.data?.message || 'Failed to load clusters'
  } finally {
    loading.value = false
  }
}

// CRITICAL: filter changes must clear items + nextCursor + cursorStack BEFORE
// re-fetching.
const loadFirstPage = () => {
  cursorStack.value = []
  loadPage('')
}

const nextPage = () => {
  if (!nextCursor.value) return
  cursorStack.value.push(nextCursor.value)
  loadPage(nextCursor.value)
}

const prevPage = () => {
  if (cursorStack.value.length === 0) return
  cursorStack.value.pop()
  const prev = cursorStack.value.length > 0 ? cursorStack.value[cursorStack.value.length - 1] : ''
  loadPage(prev)
}

const onNameLikeInput = () => {
  clearTimeout(nameLikeTimer)
  nameLikeTimer = setTimeout(() => {
    nameLikeFilter.value = nameLikeInput.value
    loadFirstPage()
  }, 300)
}

// Auto-refresh keeps the current page in sync with health-check status.
const refreshCurrentPage = () => {
  const current = cursorStack.value.length > 0 ? cursorStack.value[cursorStack.value.length - 1] : ''
  loadPage(current)
}

const loadOperators = async () => {
  try {
    const response = await apiClient.post('/nis.v1.OperatorService/ListOperators', {})
    operators.value = response.data.operators || []
  } catch (err) {
    console.error('Failed to load operators:', err)
  }
}

const showCreateModal = () => {
  editingCluster.value = null
  formData.value = {
    name: '',
    description: '',
    operatorId: '',
    serverUrls: [],
    skipVerifyTls: false
  }
  showModal.value = true
  formError.value = ''
}

const showEditModal = (cluster) => {
  editingCluster.value = cluster
  formData.value = { ...cluster }
  showModal.value = true
  formError.value = ''
}

const closeModal = () => {
  showModal.value = false
  editingCluster.value = null
  formData.value = {}
  formError.value = ''
}

const handleSubmit = async (data) => {
  saving.value = true
  formError.value = ''
  const payload = {
    ...data,
    serverUrls: (data.serverUrls || []).map(u => u.trim()).filter(u => u !== '')
  }
  try {
    if (editingCluster.value) {
      await apiClient.post('/nis.v1.ClusterService/UpdateCluster', {
        id: editingCluster.value.id,
        ...payload
      })
    } else {
      await apiClient.post('/nis.v1.ClusterService/CreateCluster', payload)
    }
    closeModal()
    await loadFirstPage()
  } catch (err) {
    formError.value = err.response?.data?.message || 'Failed to save cluster'
  } finally {
    saving.value = false
  }
}

const handleDelete = async (cluster) => {
  if (!confirm(`Are you sure you want to delete cluster "${cluster.name}"?`)) {
    return
  }

  try {
    await apiClient.post('/nis.v1.ClusterService/DeleteCluster', {
      id: cluster.id
    })
    await loadFirstPage()
  } catch (err) {
    error.value = err.response?.data?.message || 'Failed to delete cluster'
  }
}

const handleSelect = (cluster) => {
  router.push(`/clusters/${cluster.id}`)
}

const syncCluster = async (cluster) => {
  syncingClusters.value[cluster.id] = true
  syncSuccess.value = ''
  syncError.value = ''

  try {
    const response = await apiClient.post('/nis.v1.ClusterService/SyncCluster', {
      id: cluster.id
    })
    const updated = response.data.accountsUpdated || 0
    const errors = response.data.errors || []
    if (errors.length > 0) {
      const lines = errors.map(e => `  • ${e.accountName || e.accountPublicKey || 'unknown'}: ${e.error}`).join('\n')
      syncError.value =
        `Sync to "${cluster.name}" pushed ${updated}/${updated + errors.length} account(s); ${errors.length} failed:\n${lines}`
    } else if (updated === 0) {
      syncSuccess.value = `No accounts to sync to cluster "${cluster.name}".`
    } else {
      syncSuccess.value = `Successfully synced ${updated} account(s) to cluster "${cluster.name}".`
    }
  } catch (err) {
    syncError.value = err.response?.data?.message || `Failed to sync cluster "${cluster.name}"`
  } finally {
    syncingClusters.value[cluster.id] = false
  }
}

const formatDate = (dateStr) => {
  if (!dateStr) return '-'
  return new Date(dateStr).toLocaleString()
}

onMounted(() => {
  loadFirstPage()
  loadOperators()

  // Auto-refresh the current page every 30 seconds — keeps health status fresh
  // without resetting the user's pagination position.
  refreshInterval = setInterval(() => {
    refreshCurrentPage()
  }, 30000)
})

onUnmounted(() => {
  if (refreshInterval) {
    clearInterval(refreshInterval)
  }
})
</script>
