<template>
  <div class="container-fluid py-4">
    <div class="d-flex justify-content-between align-items-center mb-4">
      <h1>Background Jobs</h1>
      <button class="btn btn-outline-primary" @click="loadJobs" :disabled="loading">
        <font-awesome-icon :icon="['fas', 'rotate']" class="me-2" :class="{ 'fa-spin': loading }" />
        Refresh
      </button>
    </div>

    <!-- Filter bar -->
    <div class="card mb-4">
      <div class="card-body">
        <div class="row g-3 align-items-end">
          <div class="col-md-3">
            <label class="form-label">Job types</label>
            <select multiple v-model="filterTypes" class="form-select" style="height: 100px;">
              <option v-for="t in KNOWN_JOB_TYPES" :key="t" :value="t">{{ t }}</option>
            </select>
            <div class="form-text">Hold Ctrl/Cmd to select multiple</div>
          </div>
          <div class="col-md-3">
            <label class="form-label">Statuses</label>
            <select multiple v-model="filterStatuses" class="form-select" style="height: 100px;">
              <option v-for="s in ALL_STATUSES" :key="s.value" :value="s.value">{{ s.label }}</option>
            </select>
          </div>
          <div class="col-md-2">
            <label class="form-label">Since</label>
            <select v-model="filterSince" class="form-select">
              <option value="1h">Last 1 hour</option>
              <option value="24h">Last 24 hours</option>
              <option value="7d">Last 7 days</option>
              <option value="30d">Last 30 days</option>
              <option value="all">All time</option>
            </select>
          </div>
          <div class="col-md-2">
            <label class="form-label d-block">Show succeeded</label>
            <div class="form-check form-switch">
              <input
                class="form-check-input"
                type="checkbox"
                id="show-succeeded"
                v-model="showSucceeded"
                @change="loadJobs"
              />
              <label class="form-check-label" for="show-succeeded">
                {{ showSucceeded ? 'visible' : 'hidden' }}
              </label>
            </div>
            <div class="form-text">Off by default — keeps the list focused on rows that need attention.</div>
          </div>
          <div class="col-md-2 d-flex gap-2">
            <button class="btn btn-primary flex-fill" @click="applyFilters" :disabled="loading">Apply</button>
            <button class="btn btn-outline-secondary flex-fill" @click="clearFilters" :disabled="loading">Clear</button>
          </div>
        </div>
      </div>
    </div>

    <div v-if="error" class="alert alert-danger">{{ error }}</div>

    <div class="card">
      <div class="card-body p-0">
        <div class="table-responsive">
          <table class="table table-hover mb-0">
            <thead>
              <tr>
                <th>ID</th>
                <th>Type</th>
                <th>Status</th>
                <th>Attempts</th>
                <th>Scheduled</th>
                <th>Last error</th>
                <th class="text-end">Actions</th>
              </tr>
            </thead>
            <tbody>
              <template v-if="loading && jobs.length === 0">
                <tr><td colspan="7" class="text-center py-5"><div class="spinner-border text-primary"></div></td></tr>
              </template>
              <template v-else-if="jobs.length === 0">
                <tr><td colspan="7" class="text-center py-5 text-muted">No jobs match the current filters.</td></tr>
              </template>
              <template v-else>
                <tr v-for="job in jobs" :key="job.id" style="cursor: pointer;" @click="openDetail(job)">
                  <td><code>{{ shortId(job.id) }}</code></td>
                  <td>{{ job.type }}</td>
                  <td>
                    <span :class="statusBadgeClass(job.status)" class="badge">{{ statusLabel(job.status) }}</span>
                  </td>
                  <td>{{ job.attempts }} / {{ job.maxAttempts }}</td>
                  <td>
                    <span :title="formatExact(job.scheduledFor)">{{ formatRelative(job.scheduledFor) }}</span>
                  </td>
                  <td class="text-truncate" style="max-width: 280px;">
                    <span v-if="job.lastError" class="text-danger" :title="job.lastError">{{ job.lastError }}</span>
                    <span v-else class="text-muted">-</span>
                  </td>
                  <td class="text-end" @click.stop>
                    <button
                      v-if="canRetry(job.status)"
                      class="btn btn-sm btn-outline-primary me-1"
                      @click="retryJob(job)"
                      :disabled="acting"
                      title="Reset attempts and re-run"
                    >
                      <font-awesome-icon :icon="['fas', 'rotate']" />
                    </button>
                    <button
                      v-if="canCancel(job)"
                      class="btn btn-sm btn-outline-danger"
                      @click="cancelJob(job)"
                      :disabled="acting"
                      title="Cancel before run"
                    >
                      <font-awesome-icon :icon="['fas', 'xmark']" />
                    </button>
                  </td>
                </tr>
              </template>
            </tbody>
          </table>
        </div>
      </div>
    </div>

    <div class="mt-3 text-center" v-if="nextCursor">
      <button class="btn btn-outline-primary" @click="loadMore" :disabled="loadingMore">
        <span v-if="loadingMore" class="spinner-border spinner-border-sm me-2"></span>
        Load more
      </button>
    </div>

    <!-- Detail modal -->
    <div v-if="selectedJob" class="modal fade show d-block" tabindex="-1" style="background-color: rgba(0,0,0,0.5)">
      <div class="modal-dialog modal-lg">
        <div class="modal-content">
          <div class="modal-header">
            <h5 class="modal-title">
              <span :class="statusBadgeClass(selectedJob.status)" class="badge me-2">
                {{ statusLabel(selectedJob.status) }}
              </span>
              {{ selectedJob.type }}
            </h5>
            <button type="button" class="btn-close" @click="selectedJob = null"></button>
          </div>
          <div class="modal-body">
            <dl class="row mb-3">
              <dt class="col-sm-3">ID</dt>
              <dd class="col-sm-9"><code>{{ selectedJob.id }}</code></dd>

              <dt class="col-sm-3">Type</dt>
              <dd class="col-sm-9">{{ selectedJob.type }}</dd>

              <dt class="col-sm-3">Status</dt>
              <dd class="col-sm-9">{{ statusLabel(selectedJob.status) }}</dd>

              <dt class="col-sm-3">Attempts</dt>
              <dd class="col-sm-9">{{ selectedJob.attempts }} / {{ selectedJob.maxAttempts }}</dd>

              <dt class="col-sm-3">Dedup key</dt>
              <dd class="col-sm-9"><code v-if="selectedJob.dedupKey">{{ selectedJob.dedupKey }}</code><span v-else class="text-muted">(none)</span></dd>

              <dt class="col-sm-3">Scheduled for</dt>
              <dd class="col-sm-9">{{ formatExact(selectedJob.scheduledFor) }}</dd>

              <dt class="col-sm-3">Started at</dt>
              <dd class="col-sm-9">{{ formatExact(selectedJob.startedAt) }}</dd>

              <dt class="col-sm-3">Completed at</dt>
              <dd class="col-sm-9">{{ formatExact(selectedJob.completedAt) }}</dd>

              <dt class="col-sm-3">Locked by</dt>
              <dd class="col-sm-9">
                <code v-if="selectedJob.lockedBy">{{ selectedJob.lockedBy }}</code>
                <span v-else class="text-muted">-</span>
              </dd>

              <dt class="col-sm-3">Lease until</dt>
              <dd class="col-sm-9">{{ formatExact(selectedJob.lockedUntil) }}</dd>
            </dl>

            <div v-if="selectedJob.lastError">
              <label class="form-label fw-bold text-danger">Last error</label>
              <pre class="bg-light p-3 rounded small">{{ selectedJob.lastError }}</pre>
            </div>

            <div v-if="selectedJob.payload && selectedJob.payload !== '{}'">
              <label class="form-label fw-bold">Payload</label>
              <pre class="bg-light p-3 rounded small">{{ prettyPayload(selectedJob.payload) }}</pre>
            </div>
          </div>
          <div class="modal-footer">
            <button
              v-if="canRetry(selectedJob.status)"
              class="btn btn-outline-primary"
              @click="retryJob(selectedJob, true)"
              :disabled="acting"
            >
              <font-awesome-icon :icon="['fas', 'rotate']" class="me-2" />
              Retry
            </button>
            <button
              v-if="canCancel(selectedJob)"
              class="btn btn-outline-danger"
              @click="cancelJob(selectedJob, true)"
              :disabled="acting"
            >
              <font-awesome-icon :icon="['fas', 'xmark']" class="me-2" />
              Cancel
            </button>
            <button type="button" class="btn btn-secondary" @click="selectedJob = null">Close</button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { jobClient } from '@/utils/clients'
import { Timestamp } from '@bufbuild/protobuf'
import { JobStatus } from '@/gen/nis/v1/job_pb'

// Keep this list in sync with services/job_handlers_retention.go +
// future handler registrations. Used only to populate the filter
// dropdown — unknown types still work via the type filter.
const KNOWN_JOB_TYPES = [
  'events.retention_sweep',
  'jobs.retention_sweep',
  'revocations.retention_sweep',
  'webhook.deliver',
  'backup.sweep',
  'backup.execute',
  'jwt.expiry_sweep',
]

// Hide JOB_STATUS_UNSPECIFIED from the picker (it's the proto zero-value
// and would over-match if a user selected it).
const ALL_STATUSES = [
  { value: JobStatus.PENDING,        label: 'pending' },
  { value: JobStatus.RUNNING,        label: 'running' },
  { value: JobStatus.SUCCEEDED,      label: 'succeeded' },
  { value: JobStatus.FAILED,         label: 'failed' },
  { value: JobStatus.DEAD_LETTERED,  label: 'dead-lettered' },
  { value: JobStatus.CANCELLED,      label: 'cancelled' },
]

// Default view: non-succeeded only — operators care about what needs
// attention, not what already worked.
const DEFAULT_STATUSES = [
  JobStatus.PENDING,
  JobStatus.RUNNING,
  JobStatus.FAILED,
  JobStatus.DEAD_LETTERED,
  JobStatus.CANCELLED,
]

const jobs = ref([])
const loading = ref(false)
const loadingMore = ref(false)
const acting = ref(false)
const error = ref('')
const nextCursor = ref('')
const selectedJob = ref(null)

const filterTypes = ref([])
const filterStatuses = ref([...DEFAULT_STATUSES])
const filterSince = ref('24h')
const showSucceeded = ref(false)

function sinceTimestamp(since) {
  if (since === 'all') return undefined
  const ms = { '1h': 3600000, '24h': 86400000, '7d': 604800000, '30d': 2592000000 }[since] || 86400000
  return Timestamp.fromDate(new Date(Date.now() - ms))
}

function effectiveStatuses() {
  // If user picked an explicit set, honour it. Otherwise toggle is the
  // master switch: showSucceeded=false hides JOB_STATUS_SUCCEEDED.
  if (filterStatuses.value && filterStatuses.value.length > 0) return filterStatuses.value
  return showSucceeded.value ? [] : DEFAULT_STATUSES
}

function buildFilter(cursor) {
  const f = {
    limit: 50,
    cursor: cursor || '',
  }
  if (filterTypes.value.length > 0) f.types = filterTypes.value
  const statuses = effectiveStatuses()
  if (statuses.length > 0) f.statuses = statuses
  const ts = sinceTimestamp(filterSince.value)
  if (ts) f.since = ts
  return f
}

const loadJobs = async () => {
  loading.value = true
  error.value = ''
  jobs.value = []
  nextCursor.value = ''
  try {
    const resp = await jobClient.listJobs({ filter: buildFilter('') })
    jobs.value = resp.jobs || []
    nextCursor.value = resp.nextCursor || ''
  } catch (err) {
    error.value = err.message || 'Failed to load jobs'
  } finally {
    loading.value = false
  }
}

const loadMore = async () => {
  loadingMore.value = true
  try {
    const resp = await jobClient.listJobs({ filter: buildFilter(nextCursor.value) })
    jobs.value.push(...(resp.jobs || []))
    nextCursor.value = resp.nextCursor || ''
  } catch (err) {
    error.value = err.message || 'Failed to load more jobs'
  } finally {
    loadingMore.value = false
  }
}

const applyFilters = () => loadJobs()
const clearFilters = () => {
  filterTypes.value = []
  filterStatuses.value = [...DEFAULT_STATUSES]
  filterSince.value = '24h'
  showSucceeded.value = false
  loadJobs()
}

const openDetail = (job) => {
  selectedJob.value = job
}

const retryJob = async (job, closeModal = false) => {
  acting.value = true
  try {
    await jobClient.retryJob({ id: job.id })
    if (closeModal) selectedJob.value = null
    await loadJobs()
  } catch (err) {
    error.value = err.message || 'Failed to retry job'
  } finally {
    acting.value = false
  }
}

const cancelJob = async (job, closeModal = false) => {
  if (!confirm(`Cancel job ${shortId(job.id)} (${job.type})? Only pending jobs can be cancelled.`)) return
  acting.value = true
  try {
    await jobClient.cancelJob({ id: job.id })
    if (closeModal) selectedJob.value = null
    await loadJobs()
  } catch (err) {
    error.value = err.message || 'Failed to cancel job'
  } finally {
    acting.value = false
  }
}

function canRetry(status) {
  return status === JobStatus.FAILED || status === JobStatus.DEAD_LETTERED || status === JobStatus.CANCELLED
}

function canCancel(job) {
  return job.status === JobStatus.PENDING
}

function statusLabel(s) {
  switch (s) {
    case JobStatus.PENDING: return 'pending'
    case JobStatus.RUNNING: return 'running'
    case JobStatus.SUCCEEDED: return 'succeeded'
    case JobStatus.FAILED: return 'failed'
    case JobStatus.DEAD_LETTERED: return 'dead-lettered'
    case JobStatus.CANCELLED: return 'cancelled'
    default: return 'unknown'
  }
}

function statusBadgeClass(s) {
  switch (s) {
    case JobStatus.PENDING: return 'bg-secondary'
    case JobStatus.RUNNING: return 'bg-info text-dark'
    case JobStatus.SUCCEEDED: return 'bg-success'
    case JobStatus.FAILED: return 'bg-warning text-dark'
    case JobStatus.DEAD_LETTERED: return 'bg-danger'
    case JobStatus.CANCELLED: return 'bg-dark'
    default: return 'bg-secondary'
  }
}

function shortId(id) {
  if (!id) return ''
  return id.length > 8 ? id.substring(0, 8) : id
}

function formatExact(ts) {
  if (!ts) return '-'
  const d = ts.toDate ? ts.toDate() : new Date(ts)
  return d.toLocaleString()
}

function formatRelative(ts) {
  if (!ts) return '-'
  const d = ts.toDate ? ts.toDate() : new Date(ts)
  const diffMs = Date.now() - d.getTime()
  const future = diffMs < 0
  const absSec = Math.floor(Math.abs(diffMs) / 1000)
  if (absSec < 60) return future ? `in ${absSec}s` : `${absSec}s ago`
  const min = Math.floor(absSec / 60)
  if (min < 60) return future ? `in ${min}m` : `${min}m ago`
  const hr = Math.floor(min / 60)
  if (hr < 24) return future ? `in ${hr}h` : `${hr}h ago`
  const day = Math.floor(hr / 24)
  return future ? `in ${day}d` : `${day}d ago`
}

function prettyPayload(json) {
  if (!json) return ''
  try {
    return JSON.stringify(JSON.parse(json), null, 2)
  } catch {
    return json
  }
}

onMounted(() => {
  loadJobs()
})
</script>
