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

      <div v-if="syncError" class="alert alert-danger alert-dismissible fade show" role="alert" style="white-space: pre-line;">
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

                <dt class="col-sm-4">Health Status:</dt>
                <dd class="col-sm-8">
                  <span v-if="cluster.lastHealthCheck">
                    <span :class="cluster.healthy ? 'badge bg-success' : 'badge bg-danger'">
                      {{ cluster.healthy ? 'Healthy' : 'Unhealthy' }}
                    </span>
                    <br>
                    <small class="text-muted">Last check: {{ formatDate(cluster.lastHealthCheck) }}</small>
                    <div v-if="!cluster.healthy && cluster.healthCheckError" class="text-danger small mt-1">
                      {{ cluster.healthCheckError }}
                    </div>
                  </span>
                  <span v-else class="badge bg-secondary">
                    Unknown
                  </span>
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

      <!-- Sync drift panel (P9). Refresh is on-demand only; no auto-poll
           to avoid ambient NATS load. Matches AccountDetailView.vue's
           JetStream-usage card pattern. -->
      <div class="card mt-4">
        <div class="card-header d-flex justify-content-between align-items-center">
          <div>
            <h5 class="mb-0">Account sync status</h5>
            <small class="text-muted">Compares each account's NIS-DB JWT against what this cluster's resolver has stored.</small>
          </div>
          <div class="d-flex align-items-center gap-2">
            <div class="form-check form-switch m-0">
              <input class="form-check-input" type="checkbox" id="driftIncludeInSync" v-model="driftIncludeInSync" @change="loadDrift">
              <label class="form-check-label small" for="driftIncludeInSync">Show in-sync</label>
            </div>
            <button class="btn btn-sm btn-outline-primary" @click="loadDrift" :disabled="driftLoading">
              <span v-if="driftLoading" class="spinner-border spinner-border-sm me-2"></span>
              <font-awesome-icon v-else :icon="['fas', 'sync']" class="me-2" />
              Refresh
            </button>
          </div>
        </div>
        <div class="card-body">
          <div v-if="driftError" class="alert alert-danger">{{ driftError }}</div>

          <div v-if="!driftLoaded && !driftLoading" class="text-muted small">
            Click <strong>Refresh</strong> to scan this cluster's resolver.
          </div>

          <div v-else-if="driftLoaded && driftRows.length === 0" class="text-success small">
            <font-awesome-icon :icon="['fas', 'check-circle']" class="me-1" />
            <span v-if="driftIncludeInSync">No accounts on this operator.</span>
            <span v-else>No drifted accounts — every account is in sync.</span>
          </div>

          <div v-else-if="driftRows.length > 0" class="table-responsive">
            <table class="table table-sm align-middle mb-0">
              <thead>
                <tr>
                  <th>Account</th>
                  <th>Status</th>
                  <th>NIS JWT issued</th>
                  <th>Resolver JWT issued</th>
                  <th>Detail</th>
                  <th class="text-end">Action</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="row in driftRows" :key="row.accountId || row.accountPublicKey">
                  <td>
                    <!-- Orphans have no NIS account_id so the detail link is meaningless;
                         show the pubkey instead with an "(unknown to NIS)" hint. -->
                    <template v-if="row.status === 'DRIFT_STATUS_ORPHAN_ON_RESOLVER'">
                      <code class="small">{{ shortenPubKey(row.accountPublicKey) }}</code>
                      <br>
                      <small class="text-muted fst-italic">unknown to NIS</small>
                    </template>
                    <router-link v-else :to="`/accounts/${row.accountId}`">{{ row.accountName }}</router-link>
                  </td>
                  <td>
                    <span :class="driftBadgeClass(row.status)">{{ driftStatusLabel(row.status) }}</span>
                  </td>
                  <td><small>{{ formatIAT(row.nisJwtIat) }}</small></td>
                  <td><small>{{ formatIAT(row.resolverJwtIat) }}</small></td>
                  <td>
                    <small v-if="row.errorMessage" class="text-muted">{{ row.errorMessage }}</small>
                    <small v-else class="text-muted">-</small>
                  </td>
                  <td class="text-end">
                    <button
                      v-if="canReconcile(row.status)"
                      class="btn btn-sm btn-outline-primary"
                      :disabled="reconcilingAccountId === row.accountId"
                      @click="reconcile(row)">
                      <span v-if="reconcilingAccountId === row.accountId" class="spinner-border spinner-border-sm me-1"></span>
                      Reconcile
                    </button>
                    <button
                      v-else-if="row.status === 'DRIFT_STATUS_ORPHAN_ON_RESOLVER'"
                      class="btn btn-sm btn-outline-danger"
                      :disabled="deletingFromResolver === row.accountPublicKey"
                      @click="deleteFromResolver(row)">
                      <span v-if="deletingFromResolver === row.accountPublicKey" class="spinner-border spinner-border-sm me-1"></span>
                      Delete from resolver
                    </button>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>

          <small v-if="driftLastChecked" class="text-muted d-block mt-2">
            Last checked: {{ formatDate(driftLastChecked.toISOString()) }}
          </small>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { useRoute } from 'vue-router'
import apiClient from '@/utils/api'

const route = useRoute()
const cluster = ref(null)
const loading = ref(false)
const error = ref('')
const syncing = ref(false)
const syncSuccess = ref('')
const syncError = ref('')
let refreshInterval = null

// Drift-panel state. Loaded on demand only — no auto-poll to keep ambient
// NATS load off the operator's cluster (one connection per scan).
const driftLoading = ref(false)
const driftLoaded = ref(false)
const driftError = ref('')
const driftRows = ref([])
const driftIncludeInSync = ref(false)
const driftLastChecked = ref(null)
const reconcilingAccountId = ref('')
const deletingFromResolver = ref('')

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
    const updated = response.data.accountsUpdated || 0
    const errors = response.data.errors || []
    if (errors.length > 0) {
      const lines = errors.map(e => `  • ${e.accountName || e.accountPublicKey || 'unknown'}: ${e.error}`).join('\n')
      syncError.value =
        `Sync pushed ${updated}/${updated + errors.length} account(s); ${errors.length} failed:\n${lines}`
    } else if (updated === 0) {
      syncSuccess.value = 'No accounts to sync.'
    } else {
      syncSuccess.value = `Successfully synced ${updated} account(s) to cluster.`
    }
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

const formatIAT = (iat) => {
  // JWT iat is Unix seconds. Display in the viewer's local timezone (per
  // SKILL §2 — storage is UTC, conversion happens at the display edge).
  if (!iat || iat === '0' || iat === 0) return '-'
  const n = typeof iat === 'string' ? parseInt(iat, 10) : iat
  if (!n) return '-'
  return new Date(n * 1000).toLocaleString()
}

// Drift status enum values come back as strings in JSON (Connect-RPC's
// default encoding). Map to UI label + badge color.
const driftStatusLabel = (status) => {
  switch (status) {
    case 'DRIFT_STATUS_IN_SYNC': return 'in sync'
    case 'DRIFT_STATUS_DB_AHEAD': return 'db ahead'
    case 'DRIFT_STATUS_OUT_OF_BAND': return 'out of band'
    case 'DRIFT_STATUS_MISSING_ON_RESOLVER': return 'missing on resolver'
    case 'DRIFT_STATUS_UNREACHABLE': return 'unreachable'
    case 'DRIFT_STATUS_ORPHAN_ON_RESOLVER': return 'orphan on resolver'
    default: return status || 'unknown'
  }
}

const driftBadgeClass = (status) => {
  // Green = good; yellow = NIS knows the fix; red = something else touched
  // the resolver (operator investigation territory); grey = no signal.
  // Orange-ish for orphans — they're cleanup-able but represent a real
  // security exposure (leaked .creds under orphan JWTs still connect).
  switch (status) {
    case 'DRIFT_STATUS_IN_SYNC': return 'badge bg-success'
    case 'DRIFT_STATUS_DB_AHEAD':
    case 'DRIFT_STATUS_MISSING_ON_RESOLVER': return 'badge bg-warning text-dark'
    case 'DRIFT_STATUS_OUT_OF_BAND':
    case 'DRIFT_STATUS_ORPHAN_ON_RESOLVER': return 'badge bg-danger'
    case 'DRIFT_STATUS_UNREACHABLE': return 'badge bg-secondary'
    default: return 'badge bg-secondary'
  }
}

const canReconcile = (status) => {
  // Reconcile pushes NIS's JWT to the resolver. Useful when the resolver
  // is behind NIS (DB_AHEAD / MISSING_ON_RESOLVER) or has been touched
  // out-of-band (OUT_OF_BAND — push rewinds the resolver). Pointless for
  // IN_SYNC; impossible while UNREACHABLE; meaningless for orphans (no
  // NIS-side JWT to push — those use the Delete-from-resolver action).
  return status === 'DRIFT_STATUS_DB_AHEAD'
    || status === 'DRIFT_STATUS_MISSING_ON_RESOLVER'
    || status === 'DRIFT_STATUS_OUT_OF_BAND'
}

const shortenPubKey = (pk) => {
  // Account NKey public keys are 56 chars (e.g. ADRFL3...AR6QQ3Q). Tables
  // get cramped fast; show enough to disambiguate.
  if (!pk) return ''
  if (pk.length <= 18) return pk
  return pk.substring(0, 12) + '…' + pk.substring(pk.length - 4)
}

const deleteFromResolver = async (row) => {
  // Confirm before destructive action — orphan rows might still have live
  // .creds connected to them; deleting from the resolver kicks those off.
  if (!window.confirm(`Delete account ${row.accountPublicKey} from the resolver?\n\nAny live connections under this account JWT will be disconnected. This does not affect NIS state (no NIS row exists for this account).`)) {
    return
  }
  deletingFromResolver.value = row.accountPublicKey
  try {
    await apiClient.post('/nis.v1.ClusterService/DeleteResolverAccount', {
      clusterId: cluster.value.id,
      publicKey: row.accountPublicKey,
    })
    await loadDrift()
  } catch (err) {
    driftError.value = err.response?.data?.message || `Failed to delete ${row.accountPublicKey} from resolver`
  } finally {
    deletingFromResolver.value = ''
  }
}

const loadDrift = async () => {
  driftLoading.value = true
  driftError.value = ''
  try {
    const response = await apiClient.post('/nis.v1.ClusterService/GetClusterDriftStatus', {
      clusterId: cluster.value.id,
      includeInSync: driftIncludeInSync.value,
    })
    driftRows.value = response.data.rows || []
    driftLoaded.value = true
    driftLastChecked.value = new Date()
  } catch (err) {
    driftError.value = err.response?.data?.message || 'Failed to scan cluster drift'
  } finally {
    driftLoading.value = false
  }
}

const reconcile = async (row) => {
  reconcilingAccountId.value = row.accountId
  try {
    await apiClient.post('/nis.v1.ClusterService/ReconcileAccountOnCluster', {
      clusterId: cluster.value.id,
      accountId: row.accountId,
    })
    // Re-scan to reflect the new state. Avoids a stale "drifted" badge
    // sitting on a now-reconciled row.
    await loadDrift()
  } catch (err) {
    driftError.value = err.response?.data?.message || `Failed to reconcile ${row.accountName}`
  } finally {
    reconcilingAccountId.value = ''
  }
}

onMounted(() => {
  loadCluster()

  // Auto-refresh cluster status every 30 seconds
  refreshInterval = setInterval(() => {
    loadCluster()
  }, 30000)
})

onUnmounted(() => {
  if (refreshInterval) {
    clearInterval(refreshInterval)
  }
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
