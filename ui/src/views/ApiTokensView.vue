<template>
  <div class="container-fluid py-4">
    <div class="d-flex justify-content-between align-items-center mb-4">
      <div>
        <h1>API Tokens</h1>
        <p class="text-muted mb-0">
          Long-lived service-account credentials for automation (CI, nisctl).
          The plaintext is shown <strong>once</strong> at creation and cannot be retrieved later.
        </p>
      </div>
      <button class="btn btn-primary" @click="openCreateModal">
        <font-awesome-icon :icon="['fas', 'plus']" class="me-2" />
        Create Token
      </button>
    </div>

    <div class="mb-3 form-check form-switch">
      <input
        id="includeRevoked"
        v-model="includeRevoked"
        class="form-check-input"
        type="checkbox"
        @change="loadTokens"
      />
      <label class="form-check-label" for="includeRevoked">Show revoked tokens</label>
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
                <th>Role</th>
                <th>Scope</th>
                <th>Prefix</th>
                <th>Last Used</th>
                <th>Expires</th>
                <th>Status</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              <tr v-if="tokens.length === 0">
                <td colspan="8" class="text-center py-5 text-muted">No API tokens found</td>
              </tr>
              <tr v-for="t in tokens" :key="t.id">
                <td>
                  <strong>{{ t.name }}</strong>
                  <div v-if="t.description" class="text-muted small">{{ t.description }}</div>
                </td>
                <td><span class="badge bg-secondary">{{ t.role }}</span></td>
                <td>
                  <code v-if="t.operatorId" :title="t.operatorId">op:{{ t.operatorId.slice(0, 8) }}</code>
                  <code v-else-if="t.accountId" :title="t.accountId">acc:{{ t.accountId.slice(0, 8) }}</code>
                  <span v-else class="text-muted">global</span>
                </td>
                <td><code>{{ t.prefix }}</code></td>
                <td>{{ formatDate(t.lastUsedAt) }}</td>
                <td>{{ formatDate(t.expiresAt) || 'never' }}</td>
                <td>
                  <span v-if="t.revokedAt" class="badge bg-danger">revoked</span>
                  <span v-else-if="isExpired(t)" class="badge bg-warning text-dark">expired</span>
                  <span v-else class="badge bg-success">active</span>
                </td>
                <td>
                  <button
                    v-if="!t.revokedAt"
                    class="btn btn-sm btn-outline-warning me-1"
                    title="Revoke"
                    @click="revokeToken(t)"
                  >
                    <font-awesome-icon :icon="['fas', 'ban']" />
                  </button>
                  <button
                    class="btn btn-sm btn-outline-danger"
                    title="Delete"
                    @click="deleteToken(t)"
                  >
                    <font-awesome-icon :icon="['fas', 'trash']" />
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>

    <!-- Create Token Modal -->
    <div v-if="showCreateModal" class="modal fade show d-block" tabindex="-1" style="background-color: rgba(0,0,0,0.5)">
      <div class="modal-dialog modal-lg">
        <div class="modal-content">
          <div class="modal-header">
            <h5 class="modal-title">Create API Token</h5>
            <button type="button" class="btn-close" @click="closeCreateModal"></button>
          </div>
          <div class="modal-body">
            <div class="mb-3">
              <label class="form-label">Name <span class="text-danger">*</span></label>
              <input v-model="createForm.name" type="text" class="form-control" placeholder="ci-runner" required />
              <div class="form-text">Used to identify the token in lists. Must be unique per creator.</div>
            </div>
            <div class="mb-3">
              <label class="form-label">Description</label>
              <textarea v-model="createForm.description" class="form-control" rows="2" placeholder="What's this token for?"></textarea>
            </div>
            <div class="mb-3">
              <label class="form-label">Role <span class="text-danger">*</span></label>
              <select v-model="createForm.role" class="form-select" required>
                <option value="">Select a role...</option>
                <option v-if="authStore.isAdmin" value="admin">admin (full access)</option>
                <option value="operator-admin">operator-admin (one operator)</option>
                <option value="account-admin">account-admin (one account)</option>
              </select>
              <div class="form-text">You cannot mint a role higher than your own.</div>
            </div>
            <div v-if="createForm.role === 'operator-admin'" class="mb-3">
              <label class="form-label">Operator <span class="text-danger">*</span></label>
              <select v-model="createForm.operatorId" class="form-select" required>
                <option value="">Select an operator...</option>
                <option v-for="op in operators" :key="op.id" :value="op.id">{{ op.name }}</option>
              </select>
            </div>
            <div v-if="createForm.role === 'account-admin'" class="mb-3">
              <label class="form-label">Account ID <span class="text-danger">*</span></label>
              <input v-model="createForm.accountId" type="text" class="form-control" placeholder="account UUID" required />
            </div>
            <div class="mb-3">
              <label class="form-label">Expires In</label>
              <select v-model="createForm.expiresIn" class="form-select">
                <option value="">Never</option>
                <option value="1d">1 day</option>
                <option value="7d">7 days</option>
                <option value="30d">30 days</option>
                <option value="90d">90 days</option>
                <option value="365d">1 year</option>
              </select>
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

    <!-- Plaintext one-time display modal -->
    <div v-if="showPlaintextModal" class="modal fade show d-block" tabindex="-1" style="background-color: rgba(0,0,0,0.5)">
      <div class="modal-dialog">
        <div class="modal-content">
          <div class="modal-header bg-warning text-dark">
            <h5 class="modal-title">
              <font-awesome-icon :icon="['fas', 'exclamation-triangle']" class="me-2" />
              Save Your API Token
            </h5>
          </div>
          <div class="modal-body">
            <div class="alert alert-warning">
              This token is shown <strong>once only</strong>. Copy it now and store it securely — there is no way to retrieve it later.
            </div>
            <label class="form-label fw-bold">API Token</label>
            <div class="input-group mb-3">
              <input
                type="text"
                class="form-control font-monospace"
                readonly
                :value="createdPlaintext"
              />
              <button class="btn btn-outline-secondary" type="button" @click="copyPlaintext">
                <font-awesome-icon :icon="['fas', 'copy']" />
                {{ plaintextCopied ? 'Copied!' : 'Copy' }}
              </button>
            </div>
            <p class="text-muted small mb-0">
              Use it by exporting <code>NIS_TOKEN=&lt;value&gt;</code> in CI, or passing <code>--token</code> to nisctl.
            </p>
          </div>
          <div class="modal-footer">
            <button type="button" class="btn btn-warning" @click="closePlaintextModal">I have saved the token</button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { apiTokenClient, operatorClient } from '@/utils/clients'
import { Timestamp } from '@bufbuild/protobuf'

const authStore = useAuthStore()
const tokens = ref([])
const operators = ref([])
const loading = ref(false)
const error = ref('')
const includeRevoked = ref(false)

const showCreateModal = ref(false)
const createForm = ref({ name: '', description: '', role: '', operatorId: '', accountId: '', expiresIn: '' })
const creating = ref(false)
const createError = ref('')

const showPlaintextModal = ref(false)
const createdPlaintext = ref('')
const plaintextCopied = ref(false)

const loadOperators = async () => {
  try {
    const resp = await operatorClient.listOperators({})
    operators.value = resp.operators || []
  } catch (err) {
    // Non-fatal — only used in the operator-admin dropdown.
    console.error('Failed to load operators', err)
  }
}

const loadTokens = async () => {
  loading.value = true
  error.value = ''
  try {
    const resp = await apiTokenClient.listAPITokens({ includeRevoked: includeRevoked.value })
    tokens.value = resp.tokens || []
  } catch (err) {
    error.value = err.message || 'Failed to load API tokens'
  } finally {
    loading.value = false
  }
}

const openCreateModal = () => {
  createForm.value = { name: '', description: '', role: '', operatorId: '', accountId: '', expiresIn: '' }
  createError.value = ''
  showCreateModal.value = true
}

const closeCreateModal = () => {
  showCreateModal.value = false
  createError.value = ''
}

const handleCreate = async () => {
  if (!createForm.value.name) {
    createError.value = 'Name is required'
    return
  }
  if (!createForm.value.role) {
    createError.value = 'Role is required'
    return
  }
  if (createForm.value.role === 'operator-admin' && !createForm.value.operatorId) {
    createError.value = 'Operator is required for operator-admin tokens'
    return
  }
  if (createForm.value.role === 'account-admin' && !createForm.value.accountId) {
    createError.value = 'Account is required for account-admin tokens'
    return
  }

  creating.value = true
  createError.value = ''
  try {
    const req = {
      name: createForm.value.name,
      description: createForm.value.description,
      role: createForm.value.role,
    }
    if (createForm.value.operatorId) req.operatorId = createForm.value.operatorId
    if (createForm.value.accountId) req.accountId = createForm.value.accountId
    if (createForm.value.expiresIn) {
      const days = parseInt(createForm.value.expiresIn.replace('d', ''), 10)
      if (!Number.isNaN(days) && days > 0) {
        req.expiresAt = Timestamp.fromDate(new Date(Date.now() + days * 24 * 60 * 60 * 1000))
      }
    }
    const resp = await apiTokenClient.createAPIToken(req)
    closeCreateModal()
    await loadTokens()
    if (resp.plaintext) {
      createdPlaintext.value = resp.plaintext
      plaintextCopied.value = false
      showPlaintextModal.value = true
    }
  } catch (err) {
    createError.value = err.message || 'Failed to create token'
  } finally {
    creating.value = false
  }
}

const closePlaintextModal = () => {
  showPlaintextModal.value = false
  createdPlaintext.value = ''
}

const copyPlaintext = async () => {
  try {
    await navigator.clipboard.writeText(createdPlaintext.value)
    plaintextCopied.value = true
    setTimeout(() => { plaintextCopied.value = false }, 2000)
  } catch (err) {
    console.error('Failed to copy', err)
  }
}

const revokeToken = async (t) => {
  if (!confirm(`Revoke API token "${t.name}"? It will stop accepting requests immediately.`)) return
  try {
    await apiTokenClient.revokeAPIToken({ id: t.id })
    await loadTokens()
  } catch (err) {
    error.value = err.message || 'Failed to revoke token'
  }
}

const deleteToken = async (t) => {
  if (!confirm(`Permanently delete API token "${t.name}"? This removes the row entirely.`)) return
  try {
    await apiTokenClient.deleteAPIToken({ id: t.id })
    await loadTokens()
  } catch (err) {
    error.value = err.message || 'Failed to delete token'
  }
}

const isExpired = (t) => {
  if (!t.expiresAt) return false
  const d = t.expiresAt.toDate ? t.expiresAt.toDate() : new Date(t.expiresAt)
  return d < new Date()
}

const formatDate = (ts) => {
  if (!ts) return ''
  const d = ts.toDate ? ts.toDate() : new Date(ts)
  return d.toLocaleString()
}

onMounted(async () => {
  await loadOperators()
  await loadTokens()
})
</script>
