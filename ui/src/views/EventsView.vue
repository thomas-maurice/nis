<template>
  <div class="container-fluid py-4">
    <div class="d-flex justify-content-between align-items-center mb-4">
      <h1>Audit Log</h1>
    </div>

    <!-- Filter bar -->
    <div class="card mb-4">
      <div class="card-body">
        <div class="row g-3 align-items-end">
          <div class="col-md-4">
            <label class="form-label">Event Types</label>
            <select multiple v-model="filterTypes" class="form-select" style="height: 120px;">
              <option v-for="t in KNOWN_EVENT_TYPES" :key="t" :value="t">{{ t }}</option>
            </select>
            <div class="form-text">Hold Ctrl/Cmd to select multiple</div>
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
          <div class="col-md-3">
            <label class="form-label">Operator ID</label>
            <input v-model="filterOperatorId" type="text" class="form-control" placeholder="Optional operator ID" />
          </div>
          <div class="col-md-3 d-flex gap-2">
            <button class="btn btn-primary flex-fill" @click="applyFilters" :disabled="loading">
              <span v-if="loading" class="spinner-border spinner-border-sm me-2"></span>
              Apply
            </button>
            <button
              class="btn btn-outline-secondary flex-fill"
              @click="clearFilters"
              :disabled="loading"
              title="Reset filters and reload"
            >
              Clear
            </button>
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
                <th>Time</th>
                <th>Type</th>
                <th>Resource</th>
                <th>Actor</th>
              </tr>
            </thead>
            <tbody>
              <template v-if="loading && events.length === 0">
                <tr>
                  <td colspan="4" class="text-center py-5">
                    <div class="spinner-border text-primary" role="status"></div>
                  </td>
                </tr>
              </template>
              <template v-else-if="events.length === 0">
                <tr>
                  <td colspan="4" class="text-center py-5 text-muted">No events found</td>
                </tr>
              </template>
              <template v-else>
                <tr
                  v-for="event in events"
                  :key="event.id"
                  style="cursor: pointer;"
                  @click="openDetail(event)"
                >
                  <td>
                    <span :title="formatDateExact(event.occurredAt)">
                      {{ formatRelative(event.occurredAt) }}
                    </span>
                  </td>
                  <td>
                    <span :class="eventTypeBadgeClass(event.type)" class="badge">
                      {{ event.type }}
                    </span>
                  </td>
                  <td @click.stop>
                    <template v-if="event.resourceType || event.resourceId">
                      <router-link
                        v-if="resourceRoute(event.resourceType, event.resourceId)"
                        :to="resourceRoute(event.resourceType, event.resourceId)"
                        :title="`${event.resourceType}/${event.resourceId}`"
                      >
                        <code>{{ event.resourceType }}/{{ shortId(event.resourceId) }}</code>
                      </router-link>
                      <code v-else>{{ event.resourceType }}/{{ shortId(event.resourceId) }}</code>
                    </template>
                    <span v-else class="text-muted">-</span>
                  </td>
                  <td @click.stop>
                    <span v-if="event.actorType === 'system'" class="text-muted">system</span>
                    <router-link
                      v-else-if="event.actorType === 'user' && event.actorId"
                      to="/api-users"
                      :title="event.actorId"
                    >
                      <font-awesome-icon :icon="['fas', 'user-shield']" class="me-1" />
                      {{ actorLabel(event) }}
                    </router-link>
                    <span v-else>{{ actorLabel(event) }}</span>
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

    <!-- Event detail modal -->
    <div v-if="selectedEvent" class="modal fade show d-block" tabindex="-1" style="background-color: rgba(0,0,0,0.5)">
      <div class="modal-dialog modal-lg">
        <div class="modal-content">
          <div class="modal-header">
            <h5 class="modal-title">
              <span :class="eventTypeBadgeClass(selectedEvent.type)" class="badge me-2">
                {{ selectedEvent.type }}
              </span>
              Event Detail
            </h5>
            <button type="button" class="btn-close" @click="selectedEvent = null"></button>
          </div>
          <div class="modal-body">
            <dl class="row mb-3">
              <dt class="col-sm-3">ID:</dt>
              <dd class="col-sm-9"><code>{{ selectedEvent.id }}</code></dd>

              <dt class="col-sm-3">Time:</dt>
              <dd class="col-sm-9">{{ formatDateExact(selectedEvent.occurredAt) }}</dd>

              <dt class="col-sm-3">Type:</dt>
              <dd class="col-sm-9">{{ selectedEvent.type }}</dd>

              <dt class="col-sm-3">Resource:</dt>
              <dd class="col-sm-9">
                <template v-if="selectedEvent.resourceType || selectedEvent.resourceId">
                  <code>{{ selectedEvent.resourceType }}/{{ selectedEvent.resourceId }}</code>
                  <router-link
                    v-if="resourceRoute(selectedEvent.resourceType, selectedEvent.resourceId)"
                    :to="resourceRoute(selectedEvent.resourceType, selectedEvent.resourceId)"
                    class="ms-2 small"
                    @click="selectedEvent = null"
                  >
                    <font-awesome-icon :icon="['fas', 'arrow-up-right-from-square']" class="me-1" />
                    open {{ selectedEvent.resourceType }}
                  </router-link>
                </template>
                <span v-else class="text-muted">-</span>
              </dd>

              <dt class="col-sm-3">Actor:</dt>
              <dd class="col-sm-9">
                <span v-if="selectedEvent.actorType === 'system'" class="text-muted">system</span>
                <template v-else-if="selectedEvent.actorType === 'user' && selectedEvent.actorId">
                  <font-awesome-icon :icon="['fas', 'user-shield']" class="me-1" />
                  <span>{{ actorLabel(selectedEvent) }}</span>
                  <code class="ms-2 text-muted small">{{ selectedEvent.actorId }}</code>
                  <router-link
                    to="/api-users"
                    class="ms-2 small"
                    @click="selectedEvent = null"
                  >
                    <font-awesome-icon :icon="['fas', 'arrow-up-right-from-square']" class="me-1" />
                    open API users
                  </router-link>
                </template>
                <span v-else>{{ selectedEvent.actorType }}:{{ selectedEvent.actorId }}</span>
              </dd>

              <template v-if="selectedEvent.operatorId">
                <dt class="col-sm-3">Operator ID:</dt>
                <dd class="col-sm-9">
                  <code>{{ selectedEvent.operatorId }}</code>
                  <router-link
                    :to="`/operators/${selectedEvent.operatorId}`"
                    class="ms-2 small"
                    @click="selectedEvent = null"
                  >
                    <font-awesome-icon :icon="['fas', 'arrow-up-right-from-square']" class="me-1" />
                    open operator
                  </router-link>
                </dd>
              </template>

              <template v-if="selectedEvent.accountId">
                <dt class="col-sm-3">Account ID:</dt>
                <dd class="col-sm-9">
                  <code>{{ selectedEvent.accountId }}</code>
                  <router-link
                    :to="`/accounts/${selectedEvent.accountId}`"
                    class="ms-2 small"
                    @click="selectedEvent = null"
                  >
                    <font-awesome-icon :icon="['fas', 'arrow-up-right-from-square']" class="me-1" />
                    open account
                  </router-link>
                </dd>
              </template>
            </dl>

            <div v-if="selectedEvent.payloadJson">
              <label class="form-label fw-bold">Payload</label>
              <CodeBlock :content="parsedPayload(selectedEvent.payloadJson)" :can-copy="true" />
            </div>
          </div>
          <div class="modal-footer">
            <button type="button" class="btn btn-secondary" @click="selectedEvent = null">Close</button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { eventClient } from '@/utils/clients'
import { Timestamp } from '@bufbuild/protobuf'
import apiClient from '@/utils/api'
import CodeBlock from '@/components/CodeBlock.vue'

const KNOWN_EVENT_TYPES = [
  'account.created', 'account.updated', 'account.deleted',
  'user.created', 'user.updated', 'user.deleted',
  'operator.created', 'operator.updated', 'operator.deleted',
  'scoped_key.created', 'scoped_key.updated', 'scoped_key.deleted',
  'cluster.created', 'cluster.updated', 'cluster.deleted',
  'cluster.synced', 'cluster.sync_failed', 'cluster.health_changed',
  'webhook.test',
]

const events = ref([])
const loading = ref(false)
const loadingMore = ref(false)
const error = ref('')
const nextCursor = ref('')
const selectedEvent = ref(null)
const apiUserNames = ref({})

const filterTypes = ref([])
const filterSince = ref('24h')
const filterOperatorId = ref('')

function sinceTimestamp(since) {
  if (since === 'all') return undefined
  const ms = { '1h': 3600000, '24h': 86400000, '7d': 604800000, '30d': 2592000000 }[since] || 86400000
  const ts = new Date(Date.now() - ms)
  return Timestamp.fromDate(ts)
}

function buildFilter(cursor) {
  const f = {
    limit: 50,
    cursor: cursor || '',
  }
  if (filterTypes.value.length > 0) f.types = filterTypes.value
  if (filterOperatorId.value.trim()) f.operatorId = filterOperatorId.value.trim()
  const ts = sinceTimestamp(filterSince.value)
  if (ts) f.since = ts
  return f
}

const loadEvents = async () => {
  loading.value = true
  error.value = ''
  events.value = []
  nextCursor.value = ''
  try {
    const resp = await eventClient.listEvents({ filter: buildFilter('') })
    events.value = resp.events || []
    nextCursor.value = resp.nextCursor || ''
  } catch (err) {
    error.value = err.message || 'Failed to load events'
  } finally {
    loading.value = false
  }
}

const applyFilters = () => {
  loadEvents()
}

const clearFilters = () => {
  filterTypes.value = []
  filterOperatorId.value = ''
  filterSince.value = '24h'
  loadEvents()
}

const loadApiUsers = async () => {
  try {
    const response = await apiClient.post('/nis.v1.AuthService/ListAPIUsers', {})
    const map = {}
    for (const u of response.data.users || []) {
      map[u.id] = u.username
    }
    apiUserNames.value = map
  } catch (err) {
    // Non-fatal: the actor cell will fall back to "user:<short_uuid>".
    console.error('Failed to load API users for actor resolution', err)
  }
}

function actorLabel(event) {
  if (!event) return ''
  if (event.actorType === 'system') return 'system'
  if (event.actorType === 'user' && event.actorId) {
    const name = apiUserNames.value[event.actorId]
    if (name) return name
    return `user:${shortId(event.actorId)}`
  }
  return `${event.actorType || 'unknown'}${event.actorId ? ':' + shortId(event.actorId) : ''}`
}

const RESOURCE_ROUTE_PREFIX = {
  operator: '/operators',
  account: '/accounts',
  user: '/users',
  cluster: '/clusters',
  scoped_key: '/signing-keys',
  webhook_subscription: '/webhooks',
}

function resourceRoute(type, id) {
  if (!type || !id) return null
  const prefix = RESOURCE_ROUTE_PREFIX[type]
  if (!prefix) return null
  return `${prefix}/${id}`
}

const loadMore = async () => {
  loadingMore.value = true
  try {
    const resp = await eventClient.listEvents({ filter: buildFilter(nextCursor.value) })
    events.value.push(...(resp.events || []))
    nextCursor.value = resp.nextCursor || ''
  } catch (err) {
    error.value = err.message || 'Failed to load more events'
  } finally {
    loadingMore.value = false
  }
}

const openDetail = (event) => {
  selectedEvent.value = event
}

function shortId(id) {
  if (!id) return ''
  return id.length > 8 ? id.substring(0, 8) + '...' : id
}

function formatDateExact(ts) {
  if (!ts) return '-'
  const d = ts.toDate ? ts.toDate() : new Date(ts)
  return d.toLocaleString()
}

function formatRelative(ts) {
  if (!ts) return '-'
  const d = ts.toDate ? ts.toDate() : new Date(ts)
  const diffMs = Date.now() - d.getTime()
  const diffSec = Math.floor(diffMs / 1000)
  if (diffSec < 60) return `${diffSec}s ago`
  const diffMin = Math.floor(diffSec / 60)
  if (diffMin < 60) return `${diffMin}m ago`
  const diffHr = Math.floor(diffMin / 60)
  if (diffHr < 24) return `${diffHr}h ago`
  const diffDay = Math.floor(diffHr / 24)
  return `${diffDay}d ago`
}

function eventTypeBadgeClass(type) {
  if (!type) return 'bg-secondary'
  if (type.endsWith('.deleted') || type.includes('failed')) return 'bg-danger'
  if (type.endsWith('.created')) return 'bg-success'
  if (type.endsWith('.updated') || type.endsWith('.synced') || type.endsWith('.health_changed')) return 'bg-info'
  if (type === 'webhook.test') return 'bg-warning text-dark'
  return 'bg-secondary'
}

function parsedPayload(json) {
  if (!json) return ''
  try {
    return JSON.stringify(JSON.parse(json), null, 2)
  } catch {
    return json
  }
}

onMounted(async () => {
  await loadApiUsers()
  loadEvents()
})
</script>
