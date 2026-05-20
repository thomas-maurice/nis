<template>
  <div class="container-fluid py-4">
    <div class="d-flex justify-content-between align-items-center mb-4">
      <h1>
        <font-awesome-icon :icon="['fas', 'layer-group']" class="me-2" />
        Permission Templates
      </h1>
      <button class="btn btn-primary" @click="showCreateModal">
        <font-awesome-icon :icon="['fas', 'plus']" class="me-1" />
        Create Template
      </button>
    </div>

    <div v-if="error" class="alert alert-danger">{{ error }}</div>

    <div class="card">
      <div class="card-body">
        <div class="row align-items-center mb-3">
          <div class="col-md-6">
            <label class="form-label mb-0">Operator</label>
            <select v-model="selectedOperator" class="form-select" @change="loadTemplates">
              <option value="">Select operator...</option>
              <option v-for="op in operators" :key="op.id" :value="op.id">{{ op.name }}</option>
            </select>
          </div>
        </div>

        <div v-if="loading" class="text-center py-4">
          <div class="spinner-border text-primary" role="status"></div>
        </div>

        <div v-else-if="!selectedOperator" class="text-muted text-center py-4">
          Select an operator to list its templates.
        </div>

        <div v-else-if="templates.length === 0" class="text-muted text-center py-4">
          No templates for this operator.
        </div>

        <table v-else class="table table-hover mb-0">
          <thead>
            <tr>
              <th>Name</th>
              <th>Description</th>
              <th>Latest Version</th>
              <th>Created</th>
              <th class="text-end">Actions</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="t in templates" :key="t.id">
              <td>
                <router-link :to="`/templates/${t.id}`">{{ t.name }}</router-link>
              </td>
              <td>{{ t.description || '-' }}</td>
              <td><span class="badge bg-secondary">v{{ t.latestVersion }}</span></td>
              <td>{{ formatDate(t.createdAt) }}</td>
              <td class="text-end">
                <router-link :to="`/templates/${t.id}`" class="btn btn-sm btn-outline-secondary me-1">
                  <font-awesome-icon :icon="['fas', 'eye']" />
                </router-link>
                <button class="btn btn-sm btn-outline-danger" @click="handleDelete(t)">
                  <font-awesome-icon :icon="['fas', 'trash']" />
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- Create modal. Permissions form mirrors the SKK create flow so
         operators see familiar fields; the textareas use the same
         literal-newline binding pattern that fixed UI1 (split on \n, no
         filter-while-typing). -->
    <div v-if="showModal" class="modal show d-block" tabindex="-1" style="background: rgba(0,0,0,0.5)">
      <div class="modal-dialog modal-lg">
        <div class="modal-content">
          <div class="modal-header">
            <h5 class="modal-title">Create Template</h5>
            <button type="button" class="btn-close" @click="closeModal"></button>
          </div>
          <div class="modal-body">
            <div v-if="formError" class="alert alert-danger">{{ formError }}</div>
            <form @submit.prevent="handleSubmit">
              <div class="mb-3">
                <label class="form-label">Operator <span class="text-danger">*</span></label>
                <select v-model="formData.operatorId" class="form-select" required>
                  <option value="">Select operator...</option>
                  <option v-for="op in operators" :key="op.id" :value="op.id">{{ op.name }}</option>
                </select>
              </div>
              <div class="mb-3">
                <label class="form-label">Name <span class="text-danger">*</span></label>
                <input v-model="formData.name" type="text" class="form-control" required placeholder="e.g. service-reader" />
                <small class="text-muted">Reserved: "default", "system"</small>
              </div>
              <div class="mb-3">
                <label class="form-label">Description</label>
                <input v-model="formData.description" type="text" class="form-control" placeholder="Read-only service consumer" />
              </div>
              <div class="row">
                <div class="col-md-6 mb-3">
                  <label class="form-label">Pub Allow</label>
                  <textarea v-model="pubAllowText" class="form-control font-monospace" rows="3" :placeholder="`events.>\n_INBOX.>`"></textarea>
                </div>
                <div class="col-md-6 mb-3">
                  <label class="form-label">Pub Deny</label>
                  <textarea v-model="pubDenyText" class="form-control font-monospace" rows="3" placeholder="admin.>"></textarea>
                </div>
                <div class="col-md-6 mb-3">
                  <label class="form-label">Sub Allow</label>
                  <textarea v-model="subAllowText" class="form-control font-monospace" rows="3" :placeholder="`events.>\nmetrics.>`"></textarea>
                </div>
                <div class="col-md-6 mb-3">
                  <label class="form-label">Sub Deny</label>
                  <textarea v-model="subDenyText" class="form-control font-monospace" rows="3"></textarea>
                </div>
              </div>
              <div class="d-flex justify-content-end mb-2">
                <div class="form-check form-switch m-0">
                  <input class="form-check-input" type="checkbox" id="tplShowAdvanced" v-model="showAdvanced">
                  <label class="form-check-label small" for="tplShowAdvanced">Show advanced</label>
                </div>
              </div>

              <div v-if="showAdvanced">
                <div class="alert alert-info small">
                  <strong>Response permissions.</strong> When a NATS client
                  sends a request, NATS creates a temporary reply inbox
                  (<code>_INBOX.&lt;random&gt;</code>) and the responder needs publish
                  permission on it. Without these, you'd have to grant blanket
                  <code>pub_allow: _INBOX.&gt;</code> — letting the responder publish
                  to <em>any</em> client's inbox. With them, NATS auto-grants a
                  narrow publish on <em>just</em> the inbox of the request being
                  handled.
                  <br><br>
                  <strong>Max msgs</strong> caps how many replies that single
                  auto-grant covers; <strong>TTL</strong> caps how long it lasts
                  after request receipt. Whichever fires first revokes the
                  permission. NATS does <em>not</em> auto-extend the TTL — if a
                  streaming response runs past it, the next publish triggers an
                  async permission violation and the subscriber stops getting
                  messages.
                  <br><br>
                  Leave both at <code>0</code> to get NATS's server-side
                  defaults (<code>1 msg / 2 min</code>) — that's the right
                  call for almost every service. Auto-grant only activates
                  when the SKK using this template has a restricted
                  <code>pub_allow</code>; permissive keys (no allow list)
                  publish freely and don't need auto-grant.
                </div>
                <div class="row">
                  <div class="col-md-6 mb-3">
                    <label class="form-label">Response Max Msgs</label>
                    <input v-model.number="formData.responseMaxMsgs" type="number" min="0" class="form-control" />
                    <div class="form-text small">Replies allowed per request. <code>0</code> ⇒ NATS default (1).</div>
                  </div>
                  <div class="col-md-6 mb-3">
                    <label class="form-label">Response TTL (seconds)</label>
                    <input v-model.number="formData.responseTTLSeconds" type="number" min="0" class="form-control" />
                    <div class="form-text small">Window the auto-grant stays valid. <code>0</code> ⇒ NATS default (2 min).</div>
                  </div>
                </div>
                <div class="mb-0">
                  <label class="form-label">Change note</label>
                  <input v-model="formData.changeNote" type="text" class="form-control" placeholder="initial version" />
                  <div class="form-text small">Recorded on the new version row. Ignored on description-only edits.</div>
                </div>
              </div>
              <div class="text-end">
                <button type="button" class="btn btn-secondary me-2" @click="closeModal">Cancel</button>
                <button type="submit" class="btn btn-primary" :disabled="saving">
                  {{ saving ? 'Saving...' : 'Create' }}
                </button>
              </div>
            </form>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import apiClient from '@/utils/api'

const operators = ref([])
const templates = ref([])
const selectedOperator = ref('')
const loading = ref(false)
const error = ref('')
const showModal = ref(false)
const saving = ref(false)
const formError = ref('')
const showAdvanced = ref(false)
const formData = ref({
  operatorId: '',
  name: '',
  description: '',
  // Defaults: 0/0 (no caps on replies, no time limit). Templates are an
  // account-wide construct and per-request latency varies per service
  // — a low TTL would silently break slow handlers. Per-service tighter
  // bounds belong on the individual SKK, not the shared template.
  responseMaxMsgs: 0,
  responseTTLSeconds: 0,
  changeNote: ''
})
const pubAllowText = ref('')
const pubDenyText = ref('')
const subAllowText = ref('')
const subDenyText = ref('')

const loadOperators = async () => {
  try {
    const resp = await apiClient.post('/nis.v1.OperatorService/ListOperators', {})
    operators.value = resp.data.operators || []
    if (operators.value.length > 0 && !selectedOperator.value) {
      selectedOperator.value = operators.value[0].id
      await loadTemplates()
    }
  } catch (err) {
    error.value = err.response?.data?.message || 'Failed to load operators'
  }
}

const loadTemplates = async () => {
  if (!selectedOperator.value) {
    templates.value = []
    return
  }
  loading.value = true
  error.value = ''
  try {
    const resp = await apiClient.post('/nis.v1.TemplateService/ListTemplates', {
      operatorId: selectedOperator.value
    })
    templates.value = resp.data.templates || []
  } catch (err) {
    error.value = err.response?.data?.message || 'Failed to load templates'
  } finally {
    loading.value = false
  }
}

const showCreateModal = () => {
  formData.value = {
    operatorId: selectedOperator.value || '',
    name: '',
    description: '',
    responseMaxMsgs: 0,
    responseTTLSeconds: 0,
    changeNote: ''
  }
  pubAllowText.value = ''
  pubDenyText.value = ''
  subAllowText.value = ''
  subDenyText.value = ''
  formError.value = ''
  showAdvanced.value = false
  showModal.value = true
}

const closeModal = () => {
  showModal.value = false
  formError.value = ''
}

// Per UI1 fix: trim/filter only at submit time, not in the binding setter,
// so Enter keystrokes don't get swallowed mid-edit.
const trimSplit = (s) => (s || '').split('\n').map(x => x.trim()).filter(x => x !== '')

const handleSubmit = async () => {
  saving.value = true
  formError.value = ''
  try {
    await apiClient.post('/nis.v1.TemplateService/CreateTemplate', {
      operatorId: formData.value.operatorId,
      name: formData.value.name,
      description: formData.value.description,
      permissions: {
        pubAllow: trimSplit(pubAllowText.value),
        pubDeny: trimSplit(pubDenyText.value),
        subAllow: trimSplit(subAllowText.value),
        subDeny: trimSplit(subDenyText.value)
      },
      responsePermission: {
        maxMsgs: formData.value.responseMaxMsgs,
        // Proto field stores nanoseconds (NATS native unit); UI works in
        // seconds and converts on submit so operators never see ns.
        expires: (formData.value.responseTTLSeconds || 0) * 1_000_000_000
      },
      changeNote: formData.value.changeNote
    })
    closeModal()
    await loadTemplates()
  } catch (err) {
    formError.value = err.response?.data?.message || 'Failed to create template'
  } finally {
    saving.value = false
  }
}

const handleDelete = async (t) => {
  // Server-side guard: TemplateService.DeleteTemplate refuses with
  // FailedPrecondition when any SKK still pins the template. The error
  // bubbles up here verbatim — surface it in the page-level alert.
  if (!confirm(`Delete template "${t.name}"?\n\nThis is refused if any scoped signing key still pins this template; detach or bump them first.`)) {
    return
  }
  try {
    await apiClient.post('/nis.v1.TemplateService/DeleteTemplate', { id: t.id })
    await loadTemplates()
  } catch (err) {
    error.value = err.response?.data?.message || 'Failed to delete template'
  }
}

const formatDate = (dateStr) => {
  if (!dateStr) return '-'
  return new Date(dateStr).toLocaleString()
}

onMounted(loadOperators)
</script>
