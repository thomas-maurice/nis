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

      <div v-if="account.jetstreamLimits?.enabled" class="row g-4 mt-1">
        <div class="col-12">
          <div class="card">
            <div class="card-header d-flex justify-content-between align-items-center">
              <h5 class="mb-0">
                Live JetStream usage
                <span class="badge bg-secondary ms-2">{{ jsUsage.length }} cluster(s)</span>
              </h5>
              <div class="d-flex align-items-center gap-2">
                <span v-if="jsUsageFetchedAt" class="text-muted small">
                  Updated {{ formatRelative(jsUsageFetchedAt) }}
                </span>
                <div class="form-check form-switch mb-0">
                  <input
                    class="form-check-input"
                    type="checkbox"
                    id="jsIncludeUnhealthy"
                    v-model="jsIncludeUnhealthy"
                    :disabled="jsUsageLoading"
                  >
                  <label class="form-check-label small text-muted" for="jsIncludeUnhealthy">
                    Include unhealthy
                  </label>
                </div>
                <button
                  class="btn btn-sm btn-outline-secondary"
                  :disabled="jsUsageLoading"
                  @click="loadJSUsage"
                >
                  <span v-if="jsUsageLoading" class="spinner-border spinner-border-sm me-1" role="status"></span>
                  Refresh
                </button>
              </div>
            </div>
            <div class="card-body">
              <div v-if="jsUsageError" class="alert alert-warning mb-3">{{ jsUsageError }}</div>
              <div v-if="!jsUsageFetchedAt && !jsUsageLoading" class="text-muted">
                Click Refresh to query live usage from each attached cluster.
              </div>
              <div v-if="jsUsage.length === 0 && jsUsageFetchedAt" class="text-muted">
                No clusters attached to this operator.
              </div>
              <div v-for="c in jsUsage" :key="c.clusterId" class="mb-3 p-3 border rounded">
                <div class="d-flex justify-content-between align-items-center mb-2">
                  <h6 class="mb-0">
                    <router-link :to="`/clusters/${c.clusterId}`">{{ c.clusterName }}</router-link>
                  </h6>
                  <span :class="probeStatusBadgeClass(c.status)" :title="c.errorMessage">
                    {{ probeStatusLabel(c.status) }}
                  </span>
                </div>
                <div v-if="probeStatusIsNotActivated(c.status)" class="alert alert-info py-2 mb-2 small">
                  JetStream is enabled for this account on this cluster, but NATS
                  hasn't initialised JS state yet. NATS does this lazily on the
                  first client connect (or first JS API call) for the account.
                  Connect once — e.g. <code>nats --creds=app.creds rtt</code> —
                  and refresh.
                </div>
                <div v-if="probeStatusIsOk(c.status) || probeStatusIsNotActivated(c.status)" class="row g-3">
                  <div class="col-md-6">
                    <div class="small text-muted">Memory</div>
                    <div class="progress mt-1" style="height: 1.25rem;">
                      <div
                        class="progress-bar"
                        :class="usageBarClass(c.usage?.memoryUsed, account.jetstreamLimits?.maxMemory)"
                        role="progressbar"
                        :style="`width: ${usagePct(c.usage?.memoryUsed, account.jetstreamLimits?.maxMemory)}%`"
                      >
                        {{ formatBytes(c.usage?.memoryUsed) }} / {{ formatLimitBytes(account.jetstreamLimits?.maxMemory) }}
                      </div>
                    </div>
                  </div>
                  <div class="col-md-6">
                    <div class="small text-muted">Storage</div>
                    <div class="progress mt-1" style="height: 1.25rem;">
                      <div
                        class="progress-bar"
                        :class="usageBarClass(c.usage?.storageUsed, account.jetstreamLimits?.maxStorage)"
                        role="progressbar"
                        :style="`width: ${usagePct(c.usage?.storageUsed, account.jetstreamLimits?.maxStorage)}%`"
                      >
                        {{ formatBytes(c.usage?.storageUsed) }} / {{ formatLimitBytes(account.jetstreamLimits?.maxStorage) }}
                      </div>
                    </div>
                  </div>
                  <div class="col-md-6">
                    <div class="small text-muted">Streams</div>
                    <div class="progress mt-1" style="height: 1.25rem;">
                      <div
                        class="progress-bar"
                        :class="usageBarClass(c.usage?.streams, account.jetstreamLimits?.maxStreams)"
                        role="progressbar"
                        :style="`width: ${usagePct(c.usage?.streams, account.jetstreamLimits?.maxStreams)}%`"
                      >
                        {{ c.usage?.streams || 0 }} / {{ formatLimitCount(account.jetstreamLimits?.maxStreams) }}
                      </div>
                    </div>
                  </div>
                  <div class="col-md-6">
                    <div class="small text-muted">Consumers</div>
                    <div class="progress mt-1" style="height: 1.25rem;">
                      <div
                        class="progress-bar"
                        :class="usageBarClass(c.usage?.consumers, account.jetstreamLimits?.maxConsumers)"
                        role="progressbar"
                        :style="`width: ${usagePct(c.usage?.consumers, account.jetstreamLimits?.maxConsumers)}%`"
                      >
                        {{ c.usage?.consumers || 0 }} / {{ formatLimitCount(account.jetstreamLimits?.maxConsumers) }}
                      </div>
                    </div>
                  </div>
                </div>
                <div v-else class="text-muted small">
                  {{ c.errorMessage || 'No usage data available.' }}
                </div>
              </div>
              <p class="text-muted small mb-0 mt-2">
                Usage is queried live from each cluster via NATS. Values reflect what the cluster reports right now;
                refresh to re-poll. No auto-refresh.
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

// Live JetStream usage (P10). Manual refresh only — no auto-poll so the page
// doesn't generate ambient NATS load on every operator's clusters.
const jsUsage = ref([])
const jsUsageLoading = ref(false)
const jsUsageError = ref('')
const jsUsageFetchedAt = ref(null)
const jsIncludeUnhealthy = ref(false)

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

// --- Live JetStream usage helpers (P10) ---

const loadJSUsage = async () => {
  jsUsageLoading.value = true
  jsUsageError.value = ''
  try {
    const resp = await apiClient.post('/nis.v1.AccountService/GetAccountJetStreamUsage', {
      accountId: route.params.id,
      includeUnhealthy: jsIncludeUnhealthy.value,
    })
    jsUsage.value = resp.data.clusters || []
    jsUsageFetchedAt.value = new Date()
  } catch (err) {
    jsUsageError.value = err.response?.data?.message || 'Failed to query JetStream usage'
    jsUsage.value = []
  } finally {
    jsUsageLoading.value = false
  }
}

const PROBE_OK = 'JET_STREAM_PROBE_STATUS_OK'
const PROBE_UNREACHABLE = 'JET_STREAM_PROBE_STATUS_UNREACHABLE'
const PROBE_NO_JS = 'JET_STREAM_PROBE_STATUS_NO_JETSTREAM'
const PROBE_ACCOUNT_NOT_FOUND = 'JET_STREAM_PROBE_STATUS_ACCOUNT_NOT_FOUND'
const PROBE_NOT_ACTIVATED = 'JET_STREAM_PROBE_STATUS_NOT_ACTIVATED'

// Wire enum: ConnectRPC over JSON serializes proto enums as their string name.
// Compare against the string constants above; numeric fallback covers the
// (uncommon) case where a transport surfaces the int instead.
const probeStatusIsOk = (s) => s === PROBE_OK || s === 1
const probeStatusIsNotActivated = (s) => s === PROBE_NOT_ACTIVATED || s === 6

const probeStatusLabel = (s) => {
  switch (s) {
    case PROBE_OK:
    case 1: return 'OK'
    case PROBE_UNREACHABLE:
    case 2: return 'Unreachable'
    case PROBE_NO_JS:
    case 3: return 'No JetStream'
    case PROBE_ACCOUNT_NOT_FOUND:
    case 4: return 'Account not found on cluster'
    case PROBE_NOT_ACTIVATED:
    case 6: return 'Not activated'
    default: return 'Error'
  }
}

const probeStatusBadgeClass = (s) => {
  switch (s) {
    case PROBE_OK:
    case 1: return 'badge bg-success'
    case PROBE_UNREACHABLE:
    case 2: return 'badge bg-warning text-dark'
    case PROBE_NO_JS:
    case 3: return 'badge bg-secondary'
    case PROBE_ACCOUNT_NOT_FOUND:
    case 4: return 'badge bg-warning text-dark'
    case PROBE_NOT_ACTIVATED:
    case 6: return 'badge bg-info'
    default: return 'badge bg-danger'
  }
}

const usagePct = (used, max) => {
  const u = Number(used) || 0
  const m = Number(max) || 0
  if (m <= 0) return Math.min(100, u > 0 ? 5 : 0) // unlimited: token bar
  return Math.min(100, (u / m) * 100)
}

const usageBarClass = (used, max) => {
  const pct = usagePct(used, max)
  if (Number(max) <= 0) return 'bg-info'
  if (pct >= 90) return 'bg-danger'
  if (pct >= 75) return 'bg-warning text-dark'
  return 'bg-success'
}

const formatBytes = (n) => {
  const v = Number(n) || 0
  if (v < 1024) return `${v} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB', 'PiB']
  let i = -1
  let x = v
  while (x >= 1024 && i < units.length - 1) { x /= 1024; i++ }
  return `${x.toFixed(1)} ${units[i]}`
}

const formatLimitBytes = (v) => {
  if (v === -1 || v === '-1') return '∞'
  if (!v || v === 0) return '0'
  return formatBytes(v)
}

const formatLimitCount = (v) => {
  if (v === -1 || v === '-1') return '∞'
  return Number(v) || 0
}

const formatRelative = (d) => {
  if (!d) return ''
  const diff = Math.floor((Date.now() - d.getTime()) / 1000)
  if (diff < 5) return 'just now'
  if (diff < 60) return `${diff}s ago`
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`
  return d.toLocaleString()
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
