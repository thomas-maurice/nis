<template>
  <div class="container-fluid py-4">
    <div v-if="loading" class="text-center py-5">
      <div class="spinner-border text-primary" role="status"></div>
    </div>

    <div v-else-if="loadError" class="alert alert-danger">{{ loadError }}</div>

    <div v-else-if="subscription">
      <div class="d-flex justify-content-between align-items-center mb-4">
        <h1>{{ subscription.name }}</h1>
        <div>
          <button class="btn btn-outline-info me-2" @click="handleSendTest" :disabled="testing">
            <span v-if="testing" class="spinner-border spinner-border-sm me-2"></span>
            <font-awesome-icon v-else :icon="['fas', 'paper-plane']" class="me-2" />
            Send Test
          </button>
          <button class="btn btn-outline-danger me-2" @click="showDeleteModal = true">
            <font-awesome-icon :icon="['fas', 'trash']" class="me-2" />
            Delete
          </button>
          <router-link to="/webhooks" class="btn btn-outline-secondary">Back to Webhooks</router-link>
        </div>
      </div>

      <div v-if="toast" class="alert alert-success alert-dismissible">
        {{ toast }}
        <button type="button" class="btn-close" @click="toast = ''"></button>
      </div>

      <div v-if="saveError" class="alert alert-danger alert-dismissible">
        {{ saveError }}
        <button type="button" class="btn-close" @click="saveError = ''"></button>
      </div>

      <!-- Edit form -->
      <div class="card mb-4">
        <div class="card-header">
          <h5 class="mb-0">Edit Webhook</h5>
        </div>
        <div class="card-body">
          <div class="mb-3">
            <label class="form-label">Name <span class="text-danger">*</span></label>
            <input v-model="editForm.name" type="text" class="form-control" />
          </div>
          <div class="mb-3">
            <label class="form-label">Description</label>
            <textarea v-model="editForm.description" class="form-control" rows="2"></textarea>
          </div>
          <div class="mb-3">
            <label class="form-label">URL <span class="text-danger">*</span></label>
            <input v-model="editForm.url" type="url" class="form-control" />
          </div>
          <div class="mb-3">
            <label class="form-label">Event Types</label>
            <div class="mb-2">
              <div class="form-check">
                <input
                  id="editAllEvents"
                  v-model="editFormAllEvents"
                  type="checkbox"
                  class="form-check-input"
                />
                <label for="editAllEvents" class="form-check-label fw-bold">All events (wildcard)</label>
              </div>
            </div>
            <select
              v-if="!editFormAllEvents"
              multiple
              v-model="editForm.eventTypes"
              class="form-select"
              style="height: 140px;"
            >
              <option v-for="t in KNOWN_EVENT_TYPES" :key="t" :value="t">{{ t }}</option>
            </select>
            <div v-if="!editFormAllEvents" class="form-text">Hold Ctrl/Cmd to select multiple</div>
          </div>
          <div class="mb-3">
            <div class="form-check form-switch">
              <input
                id="editEnabled"
                v-model="editForm.enabled"
                type="checkbox"
                class="form-check-input"
              />
              <label for="editEnabled" class="form-check-label">Enabled</label>
            </div>
            <div v-if="subscription.disabledReason" class="form-text text-warning">
              Disabled reason: {{ subscription.disabledReason }}
            </div>
          </div>
          <button class="btn btn-primary" @click="handleSave" :disabled="saving">
            <span v-if="saving" class="spinner-border spinner-border-sm me-2"></span>
            Save
          </button>
        </div>
      </div>

      <!-- Deliveries panel -->
      <div class="card">
        <div class="card-header d-flex justify-content-between align-items-center">
          <h5 class="mb-0">Deliveries</h5>
          <div>
            <button
              v-for="s in ['all', 'pending', 'succeeded', 'failed', 'dead_letter']"
              :key="s"
              class="btn btn-sm me-1"
              :class="deliveryStatusFilter === s ? 'btn-primary' : 'btn-outline-secondary'"
              @click="setDeliveryFilter(s)"
            >
              {{ s === 'all' ? 'All' : statusLabel(s) }}
            </button>
          </div>
        </div>
        <div class="card-body p-0">
          <div v-if="deliveriesLoading" class="text-center py-4">
            <div class="spinner-border text-primary" role="status"></div>
          </div>
          <div v-else-if="deliveriesError" class="p-3">
            <div class="alert alert-danger mb-0">{{ deliveriesError }}</div>
          </div>
          <div v-else class="table-responsive">
            <table class="table table-hover mb-0">
              <thead>
                <tr>
                  <th>Status</th>
                  <th>Event ID</th>
                  <th>Attempt</th>
                  <th>Response Code</th>
                  <th>Last Error</th>
                  <th>Next Attempt</th>
                  <th>Completed</th>
                </tr>
              </thead>
              <tbody>
                <tr v-if="deliveries.length === 0">
                  <td colspan="7" class="text-center py-4 text-muted">No deliveries found</td>
                </tr>
                <tr v-for="d in deliveries" :key="d.id">
                  <td>
                    <span :class="deliveryStatusBadge(d.status)" class="badge">
                      {{ statusLabel(d.status) }}
                    </span>
                  </td>
                  <td>
                    <code :title="d.eventId" style="cursor: help;">{{ shortId(d.eventId) }}</code>
                  </td>
                  <td>{{ d.attempt }}</td>
                  <td>
                    <span v-if="d.lastResponseCode > 0">{{ d.lastResponseCode }}</span>
                    <span v-else class="text-muted">-</span>
                  </td>
                  <td>
                    <span
                      v-if="d.lastError"
                      class="last-error-cell"
                    >{{ d.lastError }}</span>
                    <span v-else class="text-muted">-</span>
                  </td>
                  <td>{{ formatDate(d.nextAttemptAt) }}</td>
                  <td>{{ formatDate(d.completedAt) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <div v-if="deliveriesOffset > 0 || deliveries.length === 50" class="p-3 text-center">
            <button
              v-if="deliveries.length === 50"
              class="btn btn-outline-primary"
              @click="loadMoreDeliveries"
              :disabled="deliveriesLoading"
            >
              Load more
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- Delete confirmation modal -->
    <div v-if="showDeleteModal" class="modal fade show d-block" tabindex="-1" style="background-color: rgba(0,0,0,0.5)">
      <div class="modal-dialog">
        <div class="modal-content">
          <div class="modal-header">
            <h5 class="modal-title">Delete Webhook</h5>
            <button type="button" class="btn-close" @click="showDeleteModal = false"></button>
          </div>
          <div class="modal-body">
            <p>Are you sure you want to delete webhook <strong>{{ subscription?.name }}</strong>?</p>
            <p class="text-danger">This action cannot be undone.</p>
            <div v-if="deleteError" class="alert alert-danger">{{ deleteError }}</div>
          </div>
          <div class="modal-footer">
            <button type="button" class="btn btn-secondary" @click="showDeleteModal = false">Cancel</button>
            <button type="button" class="btn btn-danger" @click="handleDelete" :disabled="deleting">
              <span v-if="deleting" class="spinner-border spinner-border-sm me-2"></span>
              Delete
            </button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { webhookClient } from '@/utils/clients'

const KNOWN_EVENT_TYPES = [
  'account.created', 'account.updated', 'account.deleted',
  'user.created', 'user.updated', 'user.deleted',
  'operator.created', 'operator.updated', 'operator.deleted',
  'scoped_key.created', 'scoped_key.updated', 'scoped_key.deleted',
  'cluster.created', 'cluster.updated', 'cluster.deleted',
  'cluster.synced', 'cluster.sync_failed', 'cluster.health_changed',
  'webhook.test',
]

const route = useRoute()
const router = useRouter()

const subscription = ref(null)
const loading = ref(false)
const loadError = ref('')
const saving = ref(false)
const saveError = ref('')
const toast = ref('')
const testing = ref(false)
const showDeleteModal = ref(false)
const deleting = ref(false)
const deleteError = ref('')

const editForm = ref({ name: '', description: '', url: '', eventTypes: [], enabled: false })
const editFormAllEvents = ref(false)

const deliveries = ref([])
const deliveriesLoading = ref(false)
const deliveriesError = ref('')
const deliveryStatusFilter = ref('all')
const deliveriesOffset = ref(0)

const loadSubscription = async () => {
  loading.value = true
  loadError.value = ''
  try {
    const resp = await webhookClient.getWebhookSubscription({ id: route.params.id })
    subscription.value = resp.subscription
    populateEditForm(resp.subscription)
  } catch (err) {
    loadError.value = err.message || 'Failed to load webhook'
  } finally {
    loading.value = false
  }
}

function populateEditForm(sub) {
  if (!sub) return
  const isAll = sub.eventTypes.length === 0 || (sub.eventTypes.length === 1 && sub.eventTypes[0] === '*')
  editFormAllEvents.value = isAll
  editForm.value = {
    name: sub.name,
    description: sub.description,
    url: sub.url,
    eventTypes: isAll ? [] : [...sub.eventTypes],
    enabled: sub.enabled,
  }
}

const handleSave = async () => {
  saving.value = true
  saveError.value = ''
  try {
    const eventTypes = editFormAllEvents.value ? ['*'] : editForm.value.eventTypes
    const resp = await webhookClient.updateWebhookSubscription({
      id: route.params.id,
      name: editForm.value.name,
      description: editForm.value.description,
      url: editForm.value.url,
      eventTypes,
      enabled: editForm.value.enabled,
    })
    subscription.value = resp.subscription
    populateEditForm(resp.subscription)
    toast.value = 'Webhook saved successfully'
    setTimeout(() => { toast.value = '' }, 3000)
  } catch (err) {
    saveError.value = err.message || 'Failed to save webhook'
  } finally {
    saving.value = false
  }
}

const handleSendTest = async () => {
  testing.value = true
  toast.value = ''
  saveError.value = ''
  try {
    const resp = await webhookClient.testWebhookSubscription({ id: route.params.id })
    toast.value = resp.deliveryId
      ? `Test dispatched (delivery id: ${resp.deliveryId})`
      : 'Test dispatched'
    setTimeout(() => { toast.value = '' }, 8000)
    await loadDeliveries()
  } catch (err) {
    saveError.value = err.message || 'Failed to send test'
  } finally {
    testing.value = false
  }
}

const handleDelete = async () => {
  deleting.value = true
  deleteError.value = ''
  try {
    await webhookClient.deleteWebhookSubscription({ id: route.params.id })
    router.push('/webhooks')
  } catch (err) {
    deleteError.value = err.message || 'Failed to delete webhook'
  } finally {
    deleting.value = false
  }
}

const loadDeliveries = async (append = false) => {
  deliveriesLoading.value = true
  deliveriesError.value = ''
  try {
    const status = deliveryStatusFilter.value === 'all' ? '' : deliveryStatusFilter.value
    const resp = await webhookClient.listWebhookDeliveries({
      subscriptionId: route.params.id,
      status,
      options: { limit: 50, offset: deliveriesOffset.value },
    })
    if (append) {
      deliveries.value.push(...(resp.deliveries || []))
    } else {
      deliveries.value = resp.deliveries || []
    }
  } catch (err) {
    deliveriesError.value = err.message || 'Failed to load deliveries'
  } finally {
    deliveriesLoading.value = false
  }
}

const setDeliveryFilter = (s) => {
  deliveryStatusFilter.value = s
  deliveriesOffset.value = 0
  loadDeliveries()
}

const loadMoreDeliveries = () => {
  deliveriesOffset.value += 50
  loadDeliveries(true)
}

function shortId(id) {
  if (!id) return '-'
  return id.length > 8 ? id.substring(0, 8) + '...' : id
}

function formatDate(ts) {
  if (!ts) return '-'
  const d = ts.toDate ? ts.toDate() : new Date(ts)
  if (isNaN(d.getTime())) return '-'
  return d.toLocaleString()
}

function deliveryStatusBadge(status) {
  switch (status) {
    case 'succeeded': return 'bg-success'
    case 'pending': return 'bg-warning text-dark'
    case 'failed': return 'bg-danger'
    case 'dead_letter': return 'bg-danger'
    default: return 'bg-secondary'
  }
}

function statusLabel(status) {
  switch (status) {
    case 'succeeded': return 'Succeeded'
    case 'pending': return 'Pending'
    case 'failed': return 'Failed'
    case 'dead_letter': return 'Dead Letter'
    default: return status
  }
}

onMounted(async () => {
  await loadSubscription()
  await loadDeliveries()
})
</script>

<style scoped>
.last-error-cell {
  display: block;
  white-space: pre-wrap;
  word-break: break-word;
  font-family: var(--bs-font-monospace);
  font-size: 0.85rem;
  max-width: 480px;
}
</style>
