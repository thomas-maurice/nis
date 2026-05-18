<template>
  <div class="container-fluid py-4">
    <div v-if="loading" class="text-center py-5">
      <div class="spinner-border text-primary" role="status"></div>
    </div>

    <div v-else-if="error" class="alert alert-danger">{{ error }}</div>

    <div v-else-if="user">
      <div class="d-flex justify-content-between align-items-center mb-4">
        <h1>{{ user.name }}</h1>
        <div>
          <button class="btn btn-outline-secondary me-2" @click="regenerateCreds" :disabled="regenerating">
            <span v-if="regenerating" class="spinner-border spinner-border-sm me-2"></span>
            <font-awesome-icon v-else :icon="['fas', 'rotate']" class="me-2" />
            Regenerate Credentials
          </button>
          <button class="btn btn-primary me-2" @click="downloadCreds">
            <font-awesome-icon :icon="['fas', 'download']" class="me-2" />
            Download Credentials
          </button>
          <button
            v-if="authStore.isAdmin || authStore.isOperatorAdmin"
            class="btn btn-danger me-2"
            @click="openRevokeModal"
            :disabled="!!user.revokedAt"
            :title="user.revokedAt ? 'Already revoked' : 'Revoke user'"
          >
            <font-awesome-icon :icon="['fas', 'ban']" class="me-2" />
            Revoke
          </button>
          <router-link to="/users" class="btn btn-outline-secondary">
            Back to Users
          </router-link>
        </div>
      </div>

      <div class="row g-4">
        <div class="col-md-6">
          <div class="card">
            <div class="card-header">
              <h5 class="mb-0">User Details</h5>
            </div>
            <div class="card-body">
              <dl class="row mb-0">
                <dt class="col-sm-4">ID:</dt>
                <dd class="col-sm-8"><code>{{ user.id }}</code></dd>

                <dt class="col-sm-4">Name:</dt>
                <dd class="col-sm-8">{{ user.name }}</dd>

                <dt class="col-sm-4">Description:</dt>
                <dd class="col-sm-8">{{ user.description || '-' }}</dd>

                <dt class="col-sm-4">Account:</dt>
                <dd class="col-sm-8">
                  <router-link v-if="account" :to="`/accounts/${account.id}`">{{ account.name }}</router-link>
                  <span v-else class="text-muted">-</span>
                </dd>

                <dt class="col-sm-4">Operator:</dt>
                <dd class="col-sm-8">
                  <router-link v-if="operator" :to="`/operators/${operator.id}`">{{ operator.name }}</router-link>
                  <span v-else class="text-muted">-</span>
                </dd>

                <dt class="col-sm-4">Public Key:</dt>
                <dd class="col-sm-8"><ClickablePubKey :pubkey="user.publicKey" /></dd>

                <dt class="col-sm-4">Created:</dt>
                <dd class="col-sm-8">{{ formatDate(user.createdAt) }}</dd>
              </dl>
            </div>
          </div>

          <!-- JWT Expiry / Revocation Status -->
          <div class="card mt-3">
            <div class="card-header">
              <h5 class="mb-0">JWT Status</h5>
            </div>
            <div class="card-body">
              <dl class="row mb-0">
                <dt class="col-sm-4">Status:</dt>
                <dd class="col-sm-8">
                  <span v-if="user.revokedAt" class="badge bg-danger">
                    Revoked &mdash; {{ user.revocationReason || 'no reason given' }}
                  </span>
                  <span v-else-if="jwtExpiredDaysAgo !== null" class="badge bg-warning text-dark">
                    Expired {{ jwtExpiredDaysAgo === 0 ? 'today' : `${jwtExpiredDaysAgo} day(s) ago` }}
                  </span>
                  <span v-else-if="jwtExpiresInDays !== null" class="badge bg-success">
                    Expires in {{ jwtExpiresInDays === 0 ? 'less than a day' : `${jwtExpiresInDays} day(s)` }}
                  </span>
                  <span v-else class="badge bg-success">Active</span>
                </dd>

                <template v-if="user.jwtIssuedAt">
                  <dt class="col-sm-4">Issued:</dt>
                  <dd class="col-sm-8">{{ formatTimestamp(user.jwtIssuedAt) }}</dd>
                </template>

                <template v-if="user.jwtExpiresAt">
                  <dt class="col-sm-4">Expires:</dt>
                  <dd class="col-sm-8">{{ formatTimestamp(user.jwtExpiresAt) }}</dd>
                </template>

                <template v-if="user.revokedAt">
                  <dt class="col-sm-4">Revoked:</dt>
                  <dd class="col-sm-8">{{ formatTimestamp(user.revokedAt) }}</dd>
                </template>
              </dl>
            </div>
          </div>
        </div>

        <div class="col-md-6">
          <div class="card">
            <div class="card-header">
              <h5 class="mb-0">User JWT</h5>
            </div>
            <div class="card-body">
              <CodeBlock :content="user.jwt" label="" />
            </div>
          </div>
        </div>
      </div>

      <div v-if="credentials" class="row mt-4">
        <div class="col-12">
          <div class="card">
            <div class="card-header">
              <h5 class="mb-0">User Credentials (.creds file)</h5>
            </div>
            <div class="card-body">
              <CodeBlock :content="credentials" label="" />
              <div class="alert alert-warning mt-3 mb-0">
                <strong>Security Notice:</strong> Keep these credentials secure. They provide full access as this user.
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Revoke confirmation modal -->
    <div v-if="showRevokeModal" class="modal fade show d-block" tabindex="-1" style="background-color: rgba(0,0,0,0.5)">
      <div class="modal-dialog">
        <div class="modal-content">
          <div class="modal-header bg-danger text-white">
            <h5 class="modal-title">
              <font-awesome-icon :icon="['fas', 'ban']" class="me-2" />
              Revoke User
            </h5>
            <button type="button" class="btn-close btn-close-white" @click="closeRevokeModal"></button>
          </div>
          <div class="modal-body">
            <p>
              Revoking <strong>{{ user?.name }}</strong> will add their public key to the parent account's revocation
              list. NATS will reject new connections from this user immediately.
            </p>
            <div class="mb-3">
              <label class="form-label" for="revokeReason">Reason</label>
              <textarea
                id="revokeReason"
                v-model="revokeReason"
                class="form-control"
                rows="3"
                placeholder="Briefly describe why this user is being revoked (optional)"
              ></textarea>
            </div>
            <div v-if="revokeError" class="alert alert-danger">{{ revokeError }}</div>
          </div>
          <div class="modal-footer">
            <button type="button" class="btn btn-secondary" @click="closeRevokeModal">Cancel</button>
            <button type="button" class="btn btn-danger" :disabled="revoking" @click="confirmRevoke">
              <span v-if="revoking" class="spinner-border spinner-border-sm me-2"></span>
              Revoke User
            </button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import apiClient from '@/utils/api'
import CodeBlock from '@/components/CodeBlock.vue'
import ClickablePubKey from '@/components/ClickablePubKey.vue'

const route = useRoute()
const authStore = useAuthStore()
const user = ref(null)
const account = ref(null)
const operator = ref(null)
const credentials = ref('')
const loading = ref(false)
const error = ref('')

// Revoke modal state
const showRevokeModal = ref(false)
const revokeReason = ref('')
const revoking = ref(false)
const revokeError = ref('')

// Regenerate state
const regenerating = ref(false)

// Computed JWT expiry values
const jwtExpiresInDays = computed(() => {
  if (!user.value?.jwtExpiresAt) return null
  const exp = timestampToDate(user.value.jwtExpiresAt)
  if (!exp) return null
  const diffMs = exp - Date.now()
  if (diffMs <= 0) return null
  return Math.floor(diffMs / 86400000)
})

const jwtExpiredDaysAgo = computed(() => {
  if (!user.value?.jwtExpiresAt) return null
  const exp = timestampToDate(user.value.jwtExpiresAt)
  if (!exp) return null
  const diffMs = Date.now() - exp
  if (diffMs <= 0) return null
  return Math.floor(diffMs / 86400000)
})

// Protobuf Timestamp from the JSON API comes back as either a string (RFC3339)
// or an object with seconds/nanos. Normalise to a Date.
const timestampToDate = (ts) => {
  if (!ts) return null
  if (typeof ts === 'string') return new Date(ts)
  if (ts.seconds !== undefined) return new Date(Number(ts.seconds) * 1000)
  return null
}

const formatTimestamp = (ts) => {
  const d = timestampToDate(ts)
  return d ? d.toLocaleString() : '-'
}

const loadUser = async () => {
  loading.value = true
  error.value = ''
  try {
    const response = await apiClient.post('/nis.v1.UserService/GetUser', {
      id: route.params.id
    })
    user.value = response.data.user

    // Load account details
    if (user.value.accountId) {
      try {
        const accResponse = await apiClient.post('/nis.v1.AccountService/GetAccount', {
          id: user.value.accountId
        })
        account.value = accResponse.data.account

        // Load operator details
        if (account.value.operatorId) {
          const opResponse = await apiClient.post('/nis.v1.OperatorService/GetOperator', {
            id: account.value.operatorId
          })
          operator.value = opResponse.data.operator
        }
      } catch (err) {
        console.error('Failed to load related entities:', err)
      }
    }
  } catch (err) {
    error.value = err.response?.data?.message || 'Failed to load user'
  } finally {
    loading.value = false
  }
}

const downloadCreds = async () => {
  try {
    const response = await apiClient.post('/nis.v1.UserService/GetUserCredentials', {
      id: user.value.id
    })
    credentials.value = response.data.credentials

    // Trigger download
    const blob = new Blob([credentials.value], { type: 'text/plain' })
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = `${user.value.name}.creds`
    link.click()
    URL.revokeObjectURL(url)
  } catch (err) {
    error.value = err.response?.data?.message || 'Failed to get credentials'
  }
}

const openRevokeModal = () => {
  revokeReason.value = ''
  revokeError.value = ''
  showRevokeModal.value = true
}

const closeRevokeModal = () => {
  showRevokeModal.value = false
  revokeError.value = ''
}

const confirmRevoke = async () => {
  revoking.value = true
  revokeError.value = ''
  try {
    const resp = await apiClient.post('/nis.v1.UserService/RevokeUser', {
      id: user.value.id,
      reason: revokeReason.value,
    })
    user.value = resp.data.user
    closeRevokeModal()
  } catch (err) {
    revokeError.value = err.response?.data?.message || 'Failed to revoke user'
  } finally {
    revoking.value = false
  }
}

const regenerateCreds = async () => {
  // NB: regenerating mints a NEW user JWT but the OLD JWT keeps working until
  // its `exp` passes. To forcibly invalidate the previous credential file,
  // revoke this user first (or instead).
  regenerating.value = true
  error.value = ''
  try {
    const resp = await apiClient.post('/nis.v1.UserService/RegenerateUserCredentials', {
      id: user.value.id,
    })
    // Render the fresh .creds inline in the existing "User Credentials" card.
    // Browser downloads are disruptive — let the operator copy/save explicitly.
    credentials.value = resp.data.credentials
    user.value = resp.data.user
  } catch (err) {
    error.value = err.response?.data?.message || 'Failed to regenerate credentials'
  } finally {
    regenerating.value = false
  }
}

const formatDate = (dateStr) => {
  if (!dateStr) return '-'
  return new Date(dateStr).toLocaleString()
}

onMounted(() => {
  loadUser()
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
