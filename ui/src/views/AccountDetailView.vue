<template>
  <div class="container-fluid py-4">
    <div v-if="loading" class="text-center py-5">
      <div class="spinner-border text-primary" role="status"></div>
    </div>

    <div v-else-if="error" class="alert alert-danger">{{ error }}</div>

    <div v-else-if="account">
      <div class="d-flex justify-content-between align-items-center mb-4">
        <h1>{{ account.name }}</h1>
        <router-link to="/accounts" class="btn btn-outline-secondary">
          Back to Accounts
        </router-link>
      </div>

      <div class="row g-4">
        <div class="col-md-6">
          <div class="card">
            <div class="card-header">
              <h5 class="mb-0">Account Details</h5>
            </div>
            <div class="card-body">
              <dl class="row mb-0">
                <dt class="col-sm-4">ID:</dt>
                <dd class="col-sm-8"><code>{{ account.id }}</code></dd>

                <dt class="col-sm-4">Name:</dt>
                <dd class="col-sm-8">{{ account.name }}</dd>

                <dt class="col-sm-4">Description:</dt>
                <dd class="col-sm-8">{{ account.description || '-' }}</dd>

                <dt class="col-sm-4">Operator:</dt>
                <dd class="col-sm-8">
                  <router-link v-if="operator" :to="`/operators/${operator.id}`">{{ operator.name }}</router-link>
                  <span v-else class="text-muted">-</span>
                </dd>

                <dt class="col-sm-4">Public Key:</dt>
                <dd class="col-sm-8"><ClickablePubKey :pubkey="account.publicKey" /></dd>

                <dt class="col-sm-4">JetStream:</dt>
                <dd class="col-sm-8">
                  <span :class="account.jetstreamLimits?.enabled ? 'badge bg-success' : 'badge bg-secondary'">
                    {{ account.jetstreamLimits?.enabled ? 'Enabled' : 'Disabled' }}
                  </span>
                </dd>

                <dt class="col-sm-4">Revoked users:</dt>
                <dd class="col-sm-8">
                  <span
                    :class="activeRevocations > 0 ? 'badge bg-danger' : 'badge bg-secondary'"
                    :title="`${activeRevocations} user(s) in this account currently flagged as revoked. Does not reflect entries still present in the account JWT's Revocations map after Regenerate Credentials.`"
                  >
                    {{ activeRevocations }}
                  </span>
                </dd>

                <dt class="col-sm-4">Created:</dt>
                <dd class="col-sm-8">{{ formatDate(account.createdAt) }}</dd>

                <dt class="col-sm-4">Account JWT exp:</dt>
                <dd class="col-sm-8">
                  <span v-if="accountJWTExp === null" class="badge bg-success" title="Account JWT has no exp; the operator has not opted into account-JWT TTL.">
                    Never expires
                  </span>
                  <span v-else-if="accountJWTExpiredDaysAgo !== null" class="badge bg-danger">
                    Expired {{ accountJWTExpiredDaysAgo === 0 ? 'today' : `${accountJWTExpiredDaysAgo} day(s) ago` }}
                    &mdash; re-sign by updating the account or via SetJWTPolicy
                  </span>
                  <span v-else class="badge bg-info">
                    Expires in {{ accountJWTExpiresInDays === 0 ? 'less than a day' : `${accountJWTExpiresInDays} day(s)` }}
                    &mdash; {{ formatDate(accountJWTExp) }}
                  </span>
                </dd>
              </dl>
            </div>
          </div>

          <div v-if="account.jetstreamLimits?.enabled" class="card mt-3">
            <div class="card-header">
              <h5 class="mb-0">JetStream Limits</h5>
            </div>
            <div class="card-body">
              <dl class="row mb-0">
                <dt class="col-sm-6">Max Memory:</dt>
                <dd class="col-sm-6">{{ formatLimit(account.jetstreamLimits?.maxMemory) }}</dd>

                <dt class="col-sm-6">Max Storage:</dt>
                <dd class="col-sm-6">{{ formatLimit(account.jetstreamLimits?.maxStorage) }}</dd>

                <dt class="col-sm-6">Max Streams:</dt>
                <dd class="col-sm-6">{{ formatLimit(account.jetstreamLimits?.maxStreams) }}</dd>

                <dt class="col-sm-6">Max Consumers:</dt>
                <dd class="col-sm-6">{{ formatLimit(account.jetstreamLimits?.maxConsumers) }}</dd>
              </dl>
            </div>
          </div>
        </div>

        <div class="col-md-6">
          <div class="card">
            <div class="card-header">
              <h5 class="mb-0">Account JWT</h5>
            </div>
            <div class="card-body">
              <CodeBlock :content="account.jwt" label="" />
            </div>
          </div>
        </div>
      </div>

      <div class="row g-4 mt-1">
        <div class="col-12">
          <div class="card">
            <div class="card-header d-flex justify-content-between align-items-center">
              <h5 class="mb-0">
                Active JWT revocations
                <span
                  class="badge ms-2"
                  :class="revocations.length > 0 ? 'bg-danger' : 'bg-secondary'"
                >
                  {{ revocations.length }}
                </span>
              </h5>
              <button
                class="btn btn-sm btn-outline-secondary"
                :disabled="revocationsLoading"
                @click="loadRevocations"
              >
                <span v-if="revocationsLoading" class="spinner-border spinner-border-sm me-1" role="status"></span>
                Refresh
              </button>
            </div>
            <div class="card-body">
              <div v-if="revocationsError" class="alert alert-warning mb-3">
                {{ revocationsError }}
              </div>
              <div v-if="revocations.length === 0 && !revocationsLoading" class="text-muted">
                No active revocations on the account JWT.
              </div>
              <div v-else-if="revocations.length > 0" class="table-responsive">
                <table class="table table-hover align-middle mb-0">
                  <thead>
                    <tr>
                      <th>User</th>
                      <th>Public key</th>
                      <th>Revoked at</th>
                      <th>JWT exp</th>
                      <th>Reason</th>
                      <th>Still flagged</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr v-for="rev in revocations" :key="rev.id">
                      <td>
                        <router-link
                          v-if="rev.userId"
                          :to="`/users/${rev.userId}`"
                        >{{ rev.userName || rev.userId }}</router-link>
                        <span v-else class="text-muted" title="User row hard-deleted; revocation outlives it until JWT exp">—</span>
                      </td>
                      <td><ClickablePubKey :pubkey="rev.userPublicKey" /></td>
                      <td>{{ formatDate(rev.revokedAt) }}</td>
                      <td>
                        <span :title="formatDate(rev.jwtExp)">
                          {{ formatJWTExp(rev.jwtExp) }}
                        </span>
                      </td>
                      <td>{{ rev.reason || '-' }}</td>
                      <td>
                        <span
                          :class="rev.userStillFlagged ? 'badge bg-warning text-dark' : 'badge bg-success'"
                          :title="rev.userStillFlagged
                            ? 'users.revoked_at is set on the user row.'
                            : 'User credential was regenerated. The revocation still applies in NATS until JWT exp.'"
                        >
                          {{ rev.userStillFlagged ? 'Yes' : 'No' }}
                        </span>
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>
              <p class="text-muted small mb-0 mt-3">
                Entries remain in the account JWT's <code>Revocations</code> map until their JWT exp elapses,
                even after Regenerate Credentials. NATS rejects connections using the revoked credential until then.
              </p>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { jwtDecode } from 'jwt-decode'
import apiClient from '@/utils/api'
import CodeBlock from '@/components/CodeBlock.vue'
import ClickablePubKey from '@/components/ClickablePubKey.vue'

const route = useRoute()
const account = ref(null)
const operator = ref(null)
const loading = ref(false)
const error = ref('')
const activeRevocations = ref(0)
const revocations = ref([])
const revocationsLoading = ref(false)
const revocationsError = ref('')

// Account JWT exp (P2). The exp lives in the signed JWT itself; decoding is
// the cheapest read. NB: jwt-decode does NOT verify the signature — it's
// strictly a JSON read of a public claim, no security implication.
const accountJWTExp = computed(() => {
  if (!account.value?.jwt) return null
  try {
    const payload = jwtDecode(account.value.jwt)
    return payload?.exp ? new Date(payload.exp * 1000) : null
  } catch {
    return null
  }
})
const accountJWTExpiresInDays = computed(() => {
  if (!accountJWTExp.value) return null
  const diff = accountJWTExp.value.getTime() - Date.now()
  if (diff <= 0) return null
  return Math.floor(diff / 86400000)
})
const accountJWTExpiredDaysAgo = computed(() => {
  if (!accountJWTExp.value) return null
  const diff = Date.now() - accountJWTExp.value.getTime()
  if (diff <= 0) return null
  return Math.floor(diff / 86400000)
})

const loadAccount = async () => {
  loading.value = true
  error.value = ''
  try {
    const response = await apiClient.post('/nis.v1.AccountService/GetAccount', {
      id: route.params.id
    })
    account.value = response.data.account

    // Load operator details
    if (account.value.operatorId) {
      try {
        const opResponse = await apiClient.post('/nis.v1.OperatorService/GetOperator', {
          id: account.value.operatorId
        })
        operator.value = opResponse.data.operator
      } catch (err) {
        console.error('Failed to load operator:', err)
      }
    }

    // Count revoked users in this account
    try {
      const usersResp = await apiClient.post('/nis.v1.UserService/ListUsers', {
        accountId: route.params.id
      })
      const users = usersResp.data.users || []
      activeRevocations.value = users.filter(u => !!u.revokedAt).length
    } catch (err) {
      // Non-fatal — badge stays 0
      console.error('Failed to load users for revocation count:', err)
    }

    await loadRevocations()
  } catch (err) {
    error.value = err.response?.data?.message || 'Failed to load account'
  } finally {
    loading.value = false
  }
}

const loadRevocations = async () => {
  revocationsLoading.value = true
  revocationsError.value = ''
  try {
    const resp = await apiClient.post('/nis.v1.AccountService/ListAccountJWTRevocations', {
      accountId: route.params.id
    })
    revocations.value = resp.data.revocations || []
  } catch (err) {
    revocationsError.value = err.response?.data?.message || 'Failed to load active revocations'
    revocations.value = []
  } finally {
    revocationsLoading.value = false
  }
}

const formatJWTExp = (dateStr) => {
  if (!dateStr) return 'Never'
  const exp = new Date(dateStr).getTime()
  const now = Date.now()
  const diff = exp - now
  const day = 86400000
  if (diff > 0) {
    const days = Math.floor(diff / day)
    if (days === 0) return 'Expires today'
    return `Expires in ${days}d`
  }
  const days = Math.floor((now - exp) / day)
  if (days === 0) return 'Expired today'
  return `Expired ${days}d ago`
}

const formatDate = (dateStr) => {
  if (!dateStr) return '-'
  return new Date(dateStr).toLocaleString()
}

const formatLimit = (value) => {
  if (value === -1 || value === '-1') return 'Unlimited'
  if (value === 0) return 'None'
  return value.toLocaleString()
}

onMounted(() => {
  loadAccount()
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
