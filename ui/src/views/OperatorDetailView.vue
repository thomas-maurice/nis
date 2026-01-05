<template>
  <div class="container-fluid py-4">
    <div v-if="loading" class="text-center py-5">
      <div class="spinner-border text-primary" role="status"></div>
    </div>

    <div v-else-if="error" class="alert alert-danger">{{ error }}</div>

    <div v-else-if="operator">
      <div class="d-flex justify-content-between align-items-center mb-4">
        <h1>{{ operator.name }}</h1>
        <div>
          <button class="btn btn-outline-success me-2" @click="showExportModal = true">
            <font-awesome-icon :icon="['fas', 'file-export']" class="me-2" />
            Export
          </button>
          <router-link to="/operators" class="btn btn-outline-secondary">
            Back to Operators
          </router-link>
        </div>
      </div>

      <div class="row g-4">
        <div class="col-md-6">
          <div class="card">
            <div class="card-header">
              <h5 class="mb-0">Operator Details</h5>
            </div>
            <div class="card-body">
              <dl class="row mb-0">
                <dt class="col-sm-4">ID:</dt>
                <dd class="col-sm-8"><code>{{ operator.id }}</code></dd>

                <dt class="col-sm-4">Name:</dt>
                <dd class="col-sm-8">{{ operator.name }}</dd>

                <dt class="col-sm-4">Description:</dt>
                <dd class="col-sm-8">{{ operator.description || '-' }}</dd>

                <dt class="col-sm-4">Public Key:</dt>
                <dd class="col-sm-8"><ClickablePubKey :pubkey="operator.publicKey" /></dd>

                <dt class="col-sm-4">System Account:</dt>
                <dd class="col-sm-8">
                  <ClickablePubKey v-if="operator.systemAccountPubKey" :pubkey="operator.systemAccountPubKey" />
                  <span v-else class="text-muted">Not set</span>
                </dd>

                <dt class="col-sm-4">Created:</dt>
                <dd class="col-sm-8">{{ formatDate(operator.createdAt) }}</dd>
              </dl>
            </div>
          </div>

          <div v-if="clusters.length > 0" class="card mt-3">
            <div class="card-header">
              <h5 class="mb-0">Clusters</h5>
            </div>
            <div class="card-body">
              <ul class="list-group">
                <li
                  v-for="cluster in clusters"
                  :key="cluster.id"
                  class="list-group-item d-flex justify-content-between align-items-center"
                >
                  <router-link :to="`/clusters/${cluster.id}`">{{ cluster.name }}</router-link>
                  <span :class="cluster.healthy ? 'badge bg-success' : 'badge bg-secondary'">
                    {{ cluster.healthy ? 'Healthy' : 'Unknown' }}
                  </span>
                </li>
              </ul>
            </div>
          </div>
        </div>

        <div class="col-md-6">
          <div class="card">
            <div class="card-header">
              <h5 class="mb-0">Operator JWT</h5>
            </div>
            <div class="card-body">
              <CodeBlock :content="operator.jwt" label="" />
            </div>
          </div>
        </div>
      </div>

      <div v-if="config" class="row mt-4">
        <div class="col-12">
          <div class="card">
            <div class="card-header d-flex justify-content-between align-items-center">
              <h5 class="mb-0">NATS Server Configuration</h5>
              <button class="btn btn-sm btn-primary" @click="downloadConfig">
                <font-awesome-icon :icon="['fas', 'download']" class="me-2" />
                Download Config
              </button>
            </div>
            <div class="card-body">
              <CodeBlock :content="config" label="" />
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Export Modal -->
    <div v-if="showExportModal" class="modal fade show d-block" tabindex="-1" style="background-color: rgba(0,0,0,0.5)">
      <div class="modal-dialog">
        <div class="modal-content">
          <div class="modal-header">
            <h5 class="modal-title">Export Operator</h5>
            <button type="button" class="btn-close" @click="closeExportModal"></button>
          </div>
          <div class="modal-body">
            <div class="mb-3">
              <div class="form-check">
                <input
                  id="includeSecrets"
                  v-model="exportIncludeSecrets"
                  class="form-check-input"
                  type="checkbox"
                />
                <label class="form-check-label" for="includeSecrets">
                  Include secrets (encrypted seeds)
                </label>
                <div class="form-text">
                  Include encrypted private keys in the export. Required for full restore.
                </div>
              </div>
            </div>
            <div v-if="exportError" class="alert alert-danger">{{ exportError }}</div>
          </div>
          <div class="modal-footer">
            <button type="button" class="btn btn-secondary" @click="closeExportModal">Cancel</button>
            <button type="button" class="btn btn-primary" @click="handleExport" :disabled="exporting">
              <span v-if="exporting" class="spinner-border spinner-border-sm me-2"></span>
              Export
            </button>
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
import CodeBlock from '@/components/CodeBlock.vue'
import ClickablePubKey from '@/components/ClickablePubKey.vue'

const route = useRoute()
const operator = ref(null)
const clusters = ref([])
const loading = ref(false)
const error = ref('')
const config = ref('')
const showExportModal = ref(false)
const exportIncludeSecrets = ref(true)
const exporting = ref(false)
const exportError = ref('')

const loadOperator = async () => {
  loading.value = true
  error.value = ''
  try {
    const response = await apiClient.post('/nis.v1.OperatorService/GetOperator', {
      id: route.params.id
    })
    operator.value = response.data.operator
    // Auto-generate config after loading operator
    await generateConfig()
    // Load clusters for this operator
    await loadClusters()
  } catch (err) {
    error.value = err.response?.data?.message || 'Failed to load operator'
  } finally {
    loading.value = false
  }
}

const loadClusters = async () => {
  try {
    const response = await apiClient.post('/nis.v1.ClusterService/ListClusters', {
      operatorId: operator.value.id
    })
    clusters.value = response.data.clusters || []
  } catch (err) {
    console.error('Failed to load clusters:', err)
  }
}

const generateConfig = async () => {
  try {
    const response = await apiClient.post('/nis.v1.OperatorService/GenerateInclude', {
      id: operator.value.id
    })
    config.value = response.data.config
  } catch (err) {
    error.value = err.response?.data?.message || 'Failed to generate config'
  }
}

const downloadConfig = () => {
  if (!config.value) return

  const blob = new Blob([config.value], { type: 'text/plain' })
  const url = window.URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `${operator.value.name}-nats-server.conf`
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  window.URL.revokeObjectURL(url)
}

const closeExportModal = () => {
  showExportModal.value = false
  exportError.value = ''
  exportIncludeSecrets.value = true
}

const handleExport = async () => {
  exporting.value = true
  exportError.value = ''
  try {
    const response = await apiClient.post('/nis.v1.ExportService/ExportOperator', {
      operatorId: operator.value.id,
      includeSecrets: exportIncludeSecrets.value
    }, {
      responseType: 'json'
    })

    // Convert the base64 data to a blob and download
    const jsonData = atob(response.data.data)
    const blob = new Blob([jsonData], { type: 'application/json' })
    const url = window.URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `${operator.value.name}-export.json`
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    window.URL.revokeObjectURL(url)

    closeExportModal()
  } catch (err) {
    exportError.value = err.response?.data?.message || 'Failed to export operator'
  } finally {
    exporting.value = false
  }
}

const formatDate = (dateStr) => {
  if (!dateStr) return '-'
  return new Date(dateStr).toLocaleString()
}

onMounted(() => {
  loadOperator()
})
</script>

<style scoped>
dt {
  font-weight: 600;
}

dd {
  margin-bottom: 0.5rem;
}
</style>
