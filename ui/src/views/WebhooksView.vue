<template>
  <div class="container-fluid py-4">
    <div class="d-flex justify-content-between align-items-center mb-4">
      <h1>Webhooks</h1>
      <button class="btn btn-primary" @click="openCreateModal">
        <font-awesome-icon :icon="['fas', 'plus']" class="me-2" />
        Create Webhook
      </button>
    </div>

    <div v-if="loading" class="text-center py-5">
      <div class="spinner-border text-primary" role="status"></div>
    </div>

    <div v-else-if="error" class="alert alert-danger">{{ error }}</div>

    <div v-else class="card">
      <div class="card-body p-0">
        <div class="table-responsive">
          <table class="table table-hover mb-0">
            <thead>
              <tr>
                <th>Name</th>
                <th>Operator</th>
                <th>URL</th>
                <th>Event Types</th>
                <th>Enabled</th>
                <th>Created</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              <tr v-if="subscriptions.length === 0">
                <td colspan="7" class="text-center py-5 text-muted">No webhooks found</td>
              </tr>
              <tr v-for="sub in subscriptions" :key="sub.id">
                <td>
                  <router-link :to="`/webhooks/${sub.id}`">{{ sub.name }}</router-link>
                </td>
                <td>
                  <span v-if="operatorNames[sub.operatorId]">{{ operatorNames[sub.operatorId] }}</span>
                  <span v-else class="text-muted">(loading...)</span>
                </td>
                <td>
                  <code class="text-truncate d-inline-block" style="max-width: 260px;" :title="sub.url">{{ sub.url }}</code>
                </td>
                <td>
                  <template v-if="sub.eventTypes.length === 0 || (sub.eventTypes.length === 1 && sub.eventTypes[0] === '*')">
                    <span class="badge bg-secondary">all</span>
                  </template>
                  <template v-else>
                    <span
                      v-for="et in sub.eventTypes.slice(0, 3)"
                      :key="et"
                      class="badge bg-info text-dark me-1"
                    >{{ et }}</span>
                    <span v-if="sub.eventTypes.length > 3" class="badge bg-secondary">+{{ sub.eventTypes.length - 3 }}</span>
                  </template>
                </td>
                <td>
                  <div class="form-check form-switch">
                    <input
                      class="form-check-input"
                      type="checkbox"
                      :checked="sub.enabled"
                      @change="toggleEnabled(sub)"
                    />
                  </div>
                </td>
                <td>{{ formatDate(sub.createdAt) }}</td>
                <td>
                  <router-link :to="`/webhooks/${sub.id}`" class="btn btn-sm btn-outline-primary me-1" title="Edit">
                    <font-awesome-icon :icon="['fas', 'edit']" />
                  </router-link>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>

    <!-- Create Webhook Modal -->
    <div v-if="showCreateModal" class="modal fade show d-block" tabindex="-1" style="background-color: rgba(0,0,0,0.5)">
      <div class="modal-dialog modal-lg">
        <div class="modal-content">
          <div class="modal-header">
            <h5 class="modal-title">Create Webhook</h5>
            <button type="button" class="btn-close" @click="closeCreateModal"></button>
          </div>
          <div class="modal-body">
            <div v-if="authStore.isAdmin" class="mb-3">
              <label class="form-label">Operator <span class="text-danger">*</span></label>
              <select v-model="createForm.operatorId" class="form-select" required>
                <option value="">Select an operator...</option>
                <option v-for="op in operators" :key="op.id" :value="op.id">{{ op.name }}</option>
              </select>
            </div>
            <div class="mb-3">
              <label class="form-label">Name <span class="text-danger">*</span></label>
              <input v-model="createForm.name" type="text" class="form-control" placeholder="my-webhook" required />
            </div>
            <div class="mb-3">
              <label class="form-label">Description</label>
              <textarea v-model="createForm.description" class="form-control" rows="2" placeholder="Optional description"></textarea>
            </div>
            <div class="mb-3">
              <label class="form-label">URL <span class="text-danger">*</span></label>
              <input v-model="createForm.url" type="url" class="form-control" placeholder="https://example.com/webhook" required />
            </div>
            <div class="mb-3">
              <label class="form-label">Event Types</label>
              <div class="mb-2">
                <div class="form-check">
                  <input
                    id="allEvents"
                    v-model="createFormAllEvents"
                    type="checkbox"
                    class="form-check-input"
                  />
                  <label for="allEvents" class="form-check-label fw-bold">All events (wildcard)</label>
                </div>
              </div>
              <select
                v-if="!createFormAllEvents"
                multiple
                v-model="createForm.eventTypes"
                class="form-select"
                style="height: 140px;"
              >
                <option v-for="t in KNOWN_EVENT_TYPES" :key="t" :value="t">{{ t }}</option>
              </select>
              <div v-if="!createFormAllEvents" class="form-text">Hold Ctrl/Cmd to select multiple</div>
            </div>
            <div v-if="createError" class="alert alert-danger">{{ createError }}</div>
          </div>
          <div class="modal-footer">
            <button type="button" class="btn btn-secondary" @click="closeCreateModal">Cancel</button>
            <button type="button" class="btn btn-primary" @click="handleCreate" :disabled="creating">
              <span v-if="creating" class="spinner-border spinner-border-sm me-2"></span>
              Create
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- Secret one-time display modal -->
    <div v-if="showSecretModal" class="modal fade show d-block" tabindex="-1" style="background-color: rgba(0,0,0,0.5)">
      <div class="modal-dialog">
        <div class="modal-content">
          <div class="modal-header bg-warning text-dark">
            <h5 class="modal-title">
              <font-awesome-icon :icon="['fas', 'exclamation-triangle']" class="me-2" />
              Save Your Webhook Secret
            </h5>
          </div>
          <div class="modal-body">
            <div class="alert alert-warning">
              This secret is shown <strong>once only</strong> and cannot be retrieved later. Copy it now and store it securely.
            </div>
            <label class="form-label fw-bold">HMAC Secret</label>
            <div class="input-group mb-3">
              <input
                type="text"
                class="form-control font-monospace"
                readonly
                :value="createdSecret"
              />
              <button class="btn btn-outline-secondary" type="button" @click="copySecret">
                <font-awesome-icon :icon="['fas', 'copy']" />
                {{ secretCopied ? 'Copied!' : 'Copy' }}
              </button>
            </div>
          </div>
          <div class="modal-footer">
            <button type="button" class="btn btn-warning" @click="closeSecretModal">I have saved the secret</button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { webhookClient } from '@/utils/clients'
import apiClient from '@/utils/api'

const KNOWN_EVENT_TYPES = [
  'account.created', 'account.updated', 'account.deleted',
  'user.created', 'user.updated', 'user.deleted',
  'operator.created', 'operator.updated', 'operator.deleted',
  'scoped_key.created', 'scoped_key.updated', 'scoped_key.deleted',
  'cluster.created', 'cluster.updated', 'cluster.deleted',
  'cluster.synced', 'cluster.sync_failed', 'cluster.health_changed',
  'webhook.test',
]

const authStore = useAuthStore()
const subscriptions = ref([])
const operators = ref([])
const operatorNames = ref({})
const loading = ref(false)
const error = ref('')

const showCreateModal = ref(false)
const createForm = ref({ operatorId: '', name: '', description: '', url: '', eventTypes: [] })
const createFormAllEvents = ref(false)
const creating = ref(false)
const createError = ref('')

const showSecretModal = ref(false)
const createdSecret = ref('')
const secretCopied = ref(false)

const loadOperators = async () => {
  try {
    const resp = await apiClient.post('/nis.v1.OperatorService/ListOperators', {})
    operators.value = resp.data.operators || []
    const names = {}
    for (const op of operators.value) {
      names[op.id] = op.name
    }
    operatorNames.value = names
  } catch (err) {
    console.error('Failed to load operators', err)
  }
}

const loadSubscriptions = async () => {
  loading.value = true
  error.value = ''
  try {
    const resp = await webhookClient.listWebhookSubscriptions({})
    subscriptions.value = resp.subscriptions || []
  } catch (err) {
    error.value = err.message || 'Failed to load webhooks'
  } finally {
    loading.value = false
  }
}

const openCreateModal = () => {
  createForm.value = { operatorId: '', name: '', description: '', url: '', eventTypes: [] }
  createFormAllEvents.value = false
  createError.value = ''
  showCreateModal.value = true
}

const closeCreateModal = () => {
  showCreateModal.value = false
  createError.value = ''
}

const handleCreate = async () => {
  if (authStore.isAdmin && !createForm.value.operatorId) {
    createError.value = 'Please select an operator'
    return
  }
  if (!createForm.value.name) {
    createError.value = 'Name is required'
    return
  }
  if (!createForm.value.url) {
    createError.value = 'URL is required'
    return
  }
  creating.value = true
  createError.value = ''
  try {
    const eventTypes = createFormAllEvents.value ? ['*'] : createForm.value.eventTypes
    const resp = await webhookClient.createWebhookSubscription({
      operatorId: createForm.value.operatorId,
      name: createForm.value.name,
      description: createForm.value.description,
      url: createForm.value.url,
      eventTypes,
    })
    closeCreateModal()
    await loadSubscriptions()
    if (resp.secret) {
      createdSecret.value = resp.secret
      secretCopied.value = false
      showSecretModal.value = true
    }
  } catch (err) {
    createError.value = err.message || 'Failed to create webhook'
  } finally {
    creating.value = false
  }
}

const closeSecretModal = () => {
  showSecretModal.value = false
  createdSecret.value = ''
}

const copySecret = async () => {
  try {
    await navigator.clipboard.writeText(createdSecret.value)
    secretCopied.value = true
    setTimeout(() => { secretCopied.value = false }, 2000)
  } catch (err) {
    console.error('Failed to copy', err)
  }
}

const toggleEnabled = async (sub) => {
  try {
    await webhookClient.updateWebhookSubscription({
      id: sub.id,
      enabled: !sub.enabled,
    })
    await loadSubscriptions()
  } catch (err) {
    error.value = err.message || 'Failed to update webhook'
  }
}

function formatDate(ts) {
  if (!ts) return '-'
  const d = ts.toDate ? ts.toDate() : new Date(ts)
  return d.toLocaleString()
}

onMounted(async () => {
  await loadOperators()
  await loadSubscriptions()
})
</script>
