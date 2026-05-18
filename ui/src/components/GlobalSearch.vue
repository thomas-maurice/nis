<template>
  <div class="global-search position-relative" ref="rootEl">
    <div class="input-group input-group-sm">
      <span class="input-group-text bg-primary border-0 text-white">
        <font-awesome-icon :icon="['fas', 'magnifying-glass']" />
      </span>
      <input
        type="text"
        class="form-control form-control-sm"
        :placeholder="placeholder"
        v-model="query"
        @input="onInput"
        @focus="open = true"
        @keydown.esc.stop="close"
      />
      <button
        v-if="query"
        class="btn btn-sm btn-outline-light"
        type="button"
        @click="reset"
        aria-label="Clear"
      >
        <font-awesome-icon :icon="['fas', 'xmark']" />
      </button>
    </div>

    <div v-if="open && (loading || results || error)" class="search-dropdown shadow">
      <div v-if="loading" class="p-3 text-muted small">
        <font-awesome-icon :icon="['fas', 'spinner']" spin class="me-2" />
        Searching…
      </div>

      <div v-else-if="error" class="p-3 text-danger small">
        {{ error }}
      </div>

      <div v-else-if="totalCount === 0" class="p-3 text-muted small">
        No matches for "{{ query }}"
      </div>

      <div v-else class="search-results">
        <div v-if="results.operators?.length" class="search-section">
          <div class="search-section-title">
            <font-awesome-icon :icon="['fas', 'server']" class="me-2" />
            Operators ({{ results.operators.length }})
          </div>
          <router-link
            v-for="o in visible(results.operators)"
            :key="'op-' + o.id"
            class="search-row"
            :to="`/operators/${o.id}`"
            @click="close"
          >
            <strong>{{ o.name }}</strong>
            <span class="text-muted small ms-2">{{ shortKey(o.publicKey) }}</span>
          </router-link>
          <div v-if="results.operators.length > maxRowsPerSection" class="search-more">
            +{{ results.operators.length - maxRowsPerSection }} more
          </div>
        </div>

        <div v-if="results.accounts?.length" class="search-section">
          <div class="search-section-title">
            <font-awesome-icon :icon="['fas', 'users']" class="me-2" />
            Accounts ({{ results.accounts.length }})
          </div>
          <router-link
            v-for="a in visible(results.accounts)"
            :key="'acc-' + a.id"
            class="search-row"
            :to="`/accounts/${a.id}`"
            @click="close"
          >
            <strong>{{ a.name }}</strong>
            <span class="text-muted small ms-2">{{ shortKey(a.publicKey) }}</span>
          </router-link>
          <div v-if="results.accounts.length > maxRowsPerSection" class="search-more">
            +{{ results.accounts.length - maxRowsPerSection }} more
          </div>
        </div>

        <div v-if="results.users?.length" class="search-section">
          <div class="search-section-title">
            <font-awesome-icon :icon="['fas', 'user']" class="me-2" />
            Users ({{ results.users.length }})
          </div>
          <router-link
            v-for="u in visible(results.users)"
            :key="'usr-' + u.id"
            class="search-row"
            :to="`/users/${u.id}`"
            @click="close"
          >
            <strong>{{ u.name }}</strong>
            <span class="text-muted small ms-2">{{ shortKey(u.publicKey) }}</span>
          </router-link>
          <div v-if="results.users.length > maxRowsPerSection" class="search-more">
            +{{ results.users.length - maxRowsPerSection }} more
          </div>
        </div>

        <div v-if="results.scopedSigningKeys?.length" class="search-section">
          <div class="search-section-title">
            <font-awesome-icon :icon="['fas', 'key']" class="me-2" />
            Signing Keys ({{ results.scopedSigningKeys.length }})
          </div>
          <router-link
            v-for="k in visible(results.scopedSigningKeys)"
            :key="'sk-' + k.id"
            class="search-row"
            :to="`/signing-keys/${k.id}`"
            @click="close"
          >
            <strong>{{ k.name }}</strong>
            <span class="text-muted small ms-2">{{ shortKey(k.publicKey) }}</span>
          </router-link>
          <div v-if="results.scopedSigningKeys.length > maxRowsPerSection" class="search-more">
            +{{ results.scopedSigningKeys.length - maxRowsPerSection }} more
          </div>
        </div>

        <div v-if="results.clusters?.length" class="search-section">
          <div class="search-section-title">
            <font-awesome-icon :icon="['fas', 'network-wired']" class="me-2" />
            Clusters ({{ results.clusters.length }})
          </div>
          <router-link
            v-for="c in visible(results.clusters)"
            :key="'cl-' + c.id"
            class="search-row"
            :to="`/clusters/${c.id}`"
            @click="close"
          >
            <strong>{{ c.name }}</strong>
            <span class="text-muted small ms-2">{{ (c.serverUrls || []).join(', ') }}</span>
          </router-link>
          <div v-if="results.clusters.length > maxRowsPerSection" class="search-more">
            +{{ results.clusters.length - maxRowsPerSection }} more
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { useRoute } from 'vue-router'
import apiClient from '@/utils/api'

const MIN_QUERY_LEN = 2
const MAX_QUERY_LEN = 128
const DEBOUNCE_MS = 250
const PER_KIND_LIMIT = 20

const placeholder = 'Search (name, key, subject)...'

const rootEl = ref(null)
const route = useRoute()

const query = ref('')
const open = ref(false)
const loading = ref(false)
const error = ref('')
const results = ref(null)
const maxRowsPerSection = 5

let debounceHandle = null
let inFlight = 0 // monotonic — drops stale responses

const totalCount = computed(() => {
  const r = results.value
  if (!r) return 0
  return (r.operators?.length || 0) + (r.accounts?.length || 0) + (r.users?.length || 0) +
    (r.scopedSigningKeys?.length || 0) + (r.clusters?.length || 0)
})

function visible(list) {
  return list.slice(0, maxRowsPerSection)
}

function shortKey(k) {
  if (!k) return ''
  if (k.length <= 14) return k
  return k.slice(0, 8) + '…' + k.slice(-4)
}

function onInput() {
  error.value = ''
  if (debounceHandle) clearTimeout(debounceHandle)
  const trimmed = query.value.trim()
  if (trimmed.length < MIN_QUERY_LEN) {
    results.value = null
    loading.value = false
    return
  }
  if (trimmed.length > MAX_QUERY_LEN) {
    error.value = `Query too long (max ${MAX_QUERY_LEN} chars)`
    results.value = null
    loading.value = false
    return
  }
  debounceHandle = setTimeout(() => runSearch(trimmed), DEBOUNCE_MS)
}

async function runSearch(q) {
  const seq = ++inFlight
  loading.value = true
  try {
    const resp = await apiClient.post('/nis.v1.SearchService/Search', {
      query: q,
      limit: PER_KIND_LIMIT
    })
    if (seq !== inFlight) return // a newer query already in flight; drop this response
    results.value = resp.data || {}
    open.value = true
  } catch (e) {
    if (seq !== inFlight) return
    const msg = e?.response?.data?.message || e?.message || 'Search failed'
    error.value = msg
    results.value = null
  } finally {
    if (seq === inFlight) loading.value = false
  }
}

function close() {
  open.value = false
}

function reset() {
  query.value = ''
  results.value = null
  error.value = ''
  open.value = false
}

function onDocClick(ev) {
  if (!rootEl.value) return
  if (!rootEl.value.contains(ev.target)) close()
}

onMounted(() => document.addEventListener('mousedown', onDocClick))
onBeforeUnmount(() => document.removeEventListener('mousedown', onDocClick))

// Auto-close when route changes (user navigated to a result).
watch(() => route.fullPath, () => {
  close()
})
</script>

<style scoped>
.global-search {
  width: 320px;
  max-width: 100%;
}

.search-dropdown {
  position: absolute;
  top: calc(100% + 6px);
  left: 0;
  right: 0;
  background: #fff;
  border: 1px solid rgba(0, 0, 0, 0.1);
  border-radius: 0.375rem;
  z-index: 1050;
  max-height: 70vh;
  overflow-y: auto;
}

.search-section {
  padding: 0.25rem 0;
  border-bottom: 1px solid rgba(0, 0, 0, 0.06);
}
.search-section:last-child {
  border-bottom: 0;
}

.search-section-title {
  padding: 0.5rem 0.75rem 0.25rem;
  font-size: 0.75rem;
  font-weight: 600;
  text-transform: uppercase;
  color: #6c757d;
  letter-spacing: 0.04em;
}

.search-row {
  display: block;
  padding: 0.4rem 0.75rem;
  color: #212529;
  text-decoration: none;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.search-row:hover {
  background: rgba(13, 110, 253, 0.08);
}

.search-more {
  padding: 0.35rem 0.75rem;
  font-size: 0.75rem;
  color: #6c757d;
  font-style: italic;
}
</style>
