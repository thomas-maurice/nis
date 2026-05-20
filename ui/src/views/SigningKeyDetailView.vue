<template>
  <div class="container-fluid py-4">
    <div v-if="loading" class="text-center py-5">
      <div class="spinner-border text-primary" role="status"></div>
    </div>

    <div v-else-if="error" class="alert alert-danger">{{ error }}</div>

    <div v-else-if="signingKey">
      <div class="d-flex justify-content-between align-items-center mb-4">
        <h1>{{ signingKey.name }}</h1>
        <router-link to="/signing-keys" class="btn btn-outline-secondary">
          Back to Signing Keys
        </router-link>
      </div>

      <div class="row g-4">
        <div class="col-md-6">
          <div class="card">
            <div class="card-header">
              <h5 class="mb-0">Signing Key Details</h5>
            </div>
            <div class="card-body">
              <dl class="row mb-0">
                <dt class="col-sm-4">ID:</dt>
                <dd class="col-sm-8"><code>{{ signingKey.id }}</code></dd>

                <dt class="col-sm-4">Name:</dt>
                <dd class="col-sm-8">{{ signingKey.name }}</dd>

                <dt class="col-sm-4">Description:</dt>
                <dd class="col-sm-8">{{ signingKey.description || '-' }}</dd>

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
                <dd class="col-sm-8"><ClickablePubKey :pubkey="signingKey.publicKey" /></dd>

                <dt class="col-sm-4">Created:</dt>
                <dd class="col-sm-8">{{ formatDate(signingKey.createdAt) }}</dd>

                <dt class="col-sm-4">Updated:</dt>
                <dd class="col-sm-8">{{ formatDate(signingKey.updatedAt) }}</dd>
              </dl>
            </div>
          </div>

          <!-- Template binding card (P6). Visible only when the SKK was
               created/bumped from a template. "Edited" + "Outdated" badges
               surface the two divergence states; the Detach button is the
               escape hatch for a SKK the operator wants to make standalone. -->
          <div v-if="signingKey.templateId" class="card mt-3">
            <div class="card-header d-flex justify-content-between align-items-center">
              <h5 class="mb-0">
                <font-awesome-icon :icon="['fas', 'link']" class="me-2" />
                Template binding
              </h5>
              <span>
                <span v-if="signingKey.trackLatest" class="badge bg-primary me-1" title="Auto-applies every new template version. Permission edits are rejected while on.">
                  Tracking latest
                </span>
                <span v-if="signingKey.templateDrifted" class="badge bg-warning text-dark me-1" title="SKK permissions were edited directly since the last bump. The next bump will overwrite them.">
                  Edited
                </span>
                <span v-if="templateOutdated" class="badge bg-info text-dark me-1" :title="`Template latest version is ${template.latestVersion}; this SKK is pinned to v${signingKey.templateVersion}`">
                  Outdated
                </span>
                <span v-else-if="template && !signingKey.trackLatest" class="badge bg-success">
                  Up to date
                </span>
              </span>
            </div>
            <div class="card-body">
              <dl class="row mb-3">
                <dt class="col-sm-4">Template:</dt>
                <dd class="col-sm-8">
                  <router-link v-if="template" :to="`/templates/${template.id}`">{{ template.name }}</router-link>
                  <span v-else class="text-muted">(unknown)</span>
                </dd>
                <dt class="col-sm-4">Pinned version:</dt>
                <dd class="col-sm-8">v{{ signingKey.templateVersion }}</dd>
                <dt v-if="template" class="col-sm-4">Latest version:</dt>
                <dd v-if="template" class="col-sm-8">v{{ template.latestVersion }}</dd>
              </dl>
              <div class="form-check form-switch mb-3">
                <input
                  class="form-check-input"
                  type="checkbox"
                  id="trackLatestToggle"
                  :checked="signingKey.trackLatest"
                  :disabled="trackToggling || signingKey.templateDrifted"
                  @change="handleTrackLatestToggle($event.target.checked)"
                />
                <label class="form-check-label" for="trackLatestToggle">
                  Auto-track template latest
                </label>
                <div class="form-text small">
                  <span v-if="signingKey.templateDrifted">
                    Disabled while drifted — bump to latest or detach first, then you can opt in.
                  </span>
                  <span v-else>
                    When on, every new template version is applied automatically and the parent account JWT is re-signed + pushed. Direct permission edits are rejected until you turn this off.
                  </span>
                </div>
              </div>
              <button
                class="btn btn-outline-danger btn-sm"
                @click="handleDetach"
                :disabled="detaching"
              >
                <font-awesome-icon :icon="['fas', 'link-slash']" class="me-1" />
                {{ detaching ? 'Detaching...' : 'Detach from template' }}
              </button>
              <p class="text-muted small mt-2 mb-0">
                Detach keeps the SKK's current permissions intact but stops
                tracking the template. Future template updates won't affect
                this SKK. Tracking is also cleared automatically.
              </p>
            </div>
          </div>
        </div>

        <div class="col-md-6">
          <div class="card">
            <div class="card-header">
              <h5 class="mb-0">Permissions</h5>
            </div>
            <div class="card-body">
              <h6 class="text-success">Publish Allow</h6>
              <PermList :subjects="signingKey.permissions?.pubAllow" empty-label="No publish allow rules" />

              <h6 class="text-danger mt-3">Publish Deny</h6>
              <PermList :subjects="signingKey.permissions?.pubDeny" empty-label="No publish deny rules" />

              <h6 class="text-success mt-3">Subscribe Allow</h6>
              <PermList :subjects="signingKey.permissions?.subAllow" empty-label="No subscribe allow rules" />

              <h6 class="text-danger mt-3">Subscribe Deny</h6>
              <PermList :subjects="signingKey.permissions?.subDeny" empty-label="No subscribe deny rules" />
            </div>
          </div>

          <div v-if="hasResponsePermission" class="card mt-3">
            <div class="card-header">
              <h5 class="mb-0">Response Permission</h5>
            </div>
            <div class="card-body">
              <dl class="row mb-0">
                <dt class="col-sm-4">Max Messages:</dt>
                <dd class="col-sm-8">{{ signingKey.responsePermission?.maxMsgs ?? 0 }}</dd>

                <dt class="col-sm-4">Expires (ns):</dt>
                <dd class="col-sm-8">{{ signingKey.responsePermission?.expires ?? 0 }}</dd>
              </dl>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, h } from 'vue'
import { useRoute } from 'vue-router'
import apiClient from '@/utils/api'
import ClickablePubKey from '@/components/ClickablePubKey.vue'

// Inline list helper — small enough to live here, not worth a separate component.
const PermList = (props) => {
  const list = props.subjects || []
  if (list.length === 0) {
    return h('div', { class: 'text-muted small' }, props.emptyLabel)
  }
  return h(
    'ul',
    { class: 'list-unstyled mb-0' },
    list.map((s) => h('li', null, [h('code', null, s)]))
  )
}
PermList.props = ['subjects', 'emptyLabel']

const route = useRoute()
const signingKey = ref(null)
const account = ref(null)
const operator = ref(null)
const template = ref(null)
const detaching = ref(false)
const trackToggling = ref(false)
const loading = ref(false)
const error = ref('')

const templateOutdated = computed(() => {
  if (!template.value || !signingKey.value?.templateVersion) return false
  return template.value.latestVersion > signingKey.value.templateVersion
})

const handleDetach = async () => {
  if (!signingKey.value?.id) return
  if (!confirm(`Detach "${signingKey.value.name}" from template "${template.value?.name ?? 'unknown'}"?\n\nThe SKK's current permissions will be preserved. Future template updates will not affect this SKK.`)) {
    return
  }
  detaching.value = true
  try {
    const resp = await apiClient.post('/nis.v1.ScopedSigningKeyService/DetachFromTemplate', {
      id: signingKey.value.id
    })
    signingKey.value = resp.data.key
    template.value = null
  } catch (err) {
    error.value = err.response?.data?.message || 'Failed to detach from template'
  } finally {
    detaching.value = false
  }
}

const handleTrackLatestToggle = async (enabled) => {
  if (!signingKey.value?.id) return
  trackToggling.value = true
  try {
    const resp = await apiClient.post('/nis.v1.ScopedSigningKeyService/SetTrackLatest', {
      id: signingKey.value.id,
      enabled
    })
    signingKey.value = resp.data.key
    error.value = ''
  } catch (err) {
    error.value = err.response?.data?.message || 'Failed to toggle track_latest'
  } finally {
    trackToggling.value = false
  }
}

const hasResponsePermission = computed(() => {
  const rp = signingKey.value?.responsePermission
  if (!rp) return false
  return (rp.maxMsgs && rp.maxMsgs > 0) || (rp.expires && rp.expires > 0)
})

const loadKey = async () => {
  loading.value = true
  error.value = ''
  try {
    const response = await apiClient.post('/nis.v1.ScopedSigningKeyService/GetScopedSigningKey', {
      id: route.params.id
    })
    signingKey.value = response.data.key

    // Resolve template metadata when pinned, so the binding card can
    // show the template name + latest version for the outdated badge.
    if (signingKey.value?.templateId) {
      try {
        const tplResp = await apiClient.post('/nis.v1.TemplateService/GetTemplate', {
          id: signingKey.value.templateId
        })
        template.value = tplResp.data.template
      } catch (err) {
        console.error('Failed to load template:', err)
      }
    }

    if (signingKey.value?.accountId) {
      try {
        const accResponse = await apiClient.post('/nis.v1.AccountService/GetAccount', {
          id: signingKey.value.accountId
        })
        account.value = accResponse.data.account

        if (account.value?.operatorId) {
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
    error.value = err.response?.data?.message || 'Failed to load signing key'
  } finally {
    loading.value = false
  }
}

const formatDate = (dateStr) => {
  if (!dateStr) return '-'
  return new Date(dateStr).toLocaleString()
}

onMounted(() => {
  loadKey()
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
