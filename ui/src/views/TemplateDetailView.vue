<template>
  <div class="container-fluid py-4">
    <div v-if="loading" class="text-center py-5">
      <div class="spinner-border text-primary" role="status"></div>
    </div>

    <div v-else-if="error" class="alert alert-danger">{{ error }}</div>

    <div v-else-if="template">
      <div class="d-flex justify-content-between align-items-center mb-4">
        <h1>
          <font-awesome-icon :icon="['fas', 'layer-group']" class="me-2" />
          {{ template.name }}
          <span class="badge bg-secondary fs-6 ms-2">v{{ template.latestVersion }}</span>
        </h1>
        <div>
          <button class="btn btn-primary me-2" @click="openEditModal">
            <font-awesome-icon :icon="['fas', 'edit']" class="me-1" />
            Edit
          </button>
          <router-link to="/templates" class="btn btn-outline-secondary">
            Back to Templates
          </router-link>
        </div>
      </div>

      <div class="row g-4">
        <div class="col-md-6">
          <div class="card">
            <div class="card-header"><h5 class="mb-0">Template Details</h5></div>
            <div class="card-body">
              <dl class="row mb-0">
                <dt class="col-sm-4">ID:</dt>
                <dd class="col-sm-8"><code>{{ template.id }}</code></dd>
                <dt class="col-sm-4">Name:</dt>
                <dd class="col-sm-8">{{ template.name }}</dd>
                <dt class="col-sm-4">Description:</dt>
                <dd class="col-sm-8">{{ template.description || '-' }}</dd>
                <dt class="col-sm-4">Operator:</dt>
                <dd class="col-sm-8">
                  <router-link v-if="template.operatorId" :to="`/operators/${template.operatorId}`">{{ template.operatorId.slice(0, 8) }}...</router-link>
                </dd>
                <dt class="col-sm-4">Latest version:</dt>
                <dd class="col-sm-8">v{{ template.latestVersion }}</dd>
                <dt class="col-sm-4">Created:</dt>
                <dd class="col-sm-8">{{ formatDate(template.createdAt) }}</dd>
                <dt class="col-sm-4">Updated:</dt>
                <dd class="col-sm-8">{{ formatDate(template.updatedAt) }}</dd>
              </dl>
            </div>
          </div>
        </div>

        <div class="col-md-6">
          <div class="card">
            <div class="card-header">
              <h5 class="mb-0">Current version (v{{ currentVersion?.versionNumber ?? '-' }})</h5>
            </div>
            <div class="card-body">
              <h6 class="text-success">Publish Allow</h6>
              <PermList :subjects="currentVersion?.permissions?.pubAllow" />
              <h6 class="text-danger mt-3">Publish Deny</h6>
              <PermList :subjects="currentVersion?.permissions?.pubDeny" />
              <h6 class="text-success mt-3">Subscribe Allow</h6>
              <PermList :subjects="currentVersion?.permissions?.subAllow" />
              <h6 class="text-danger mt-3">Subscribe Deny</h6>
              <PermList :subjects="currentVersion?.permissions?.subDeny" />
            </div>
          </div>
        </div>
      </div>

      <div class="row g-4 mt-1">
        <div class="col-md-6">
          <div class="card">
            <div class="card-header"><h5 class="mb-0">Version history</h5></div>
            <div class="card-body p-0">
              <table class="table mb-0">
                <thead>
                  <tr>
                    <th>Version</th>
                    <th>Created</th>
                    <th>Change note</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="v in versions" :key="v.id">
                    <td><span class="badge bg-secondary">v{{ v.versionNumber }}</span></td>
                    <td>{{ formatDate(v.createdAt) }}</td>
                    <td><small>{{ v.changeNote || '-' }}</small></td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
        </div>

        <!-- Dependents: which SSKs currently pin this template. Operators
             use this to scope a rollout — "bump these N keys to v4" — and
             to spot drifted keys that would silently get overwritten on a
             bump. -->
        <div class="col-md-6">
          <div class="card">
            <div class="card-header">
              <h5 class="mb-0">
                <font-awesome-icon :icon="['fas', 'link']" class="me-2" />
                Dependent scoped signing keys
                <span class="badge bg-info text-dark ms-1">{{ dependents.length }}</span>
              </h5>
            </div>
            <div class="card-body p-0">
              <div v-if="dependents.length === 0" class="p-3 text-muted">No SSKs are currently pinned to this template.</div>
              <table v-else class="table mb-0">
                <thead>
                  <tr>
                    <th>Account</th>
                    <th>SSK name</th>
                    <th>Pinned</th>
                    <th></th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="d in dependents" :key="d.scopedSigningKeyId">
                    <td>{{ d.accountName }}</td>
                    <td>
                      <router-link :to="`/signing-keys/${d.scopedSigningKeyId}`">{{ d.scopedSigningKeyName }}</router-link>
                    </td>
                    <td>
                      <span class="badge" :class="d.pinnedVersion === template.latestVersion ? 'bg-success' : 'bg-info text-dark'">
                        v{{ d.pinnedVersion }}
                      </span>
                      <span v-if="d.drifted" class="badge bg-warning text-dark ms-1" title="permissions edited directly since last bump">
                        edited
                      </span>
                    </td>
                    <td class="text-end">
                      <button
                        v-if="d.pinnedVersion !== template.latestVersion"
                        class="btn btn-sm btn-outline-primary"
                        @click="handleBump(d)"
                        :disabled="bumpingId === d.scopedSigningKeyId"
                        :title="`Apply v${template.latestVersion} to this SSK and push to NATS`"
                      >
                        <font-awesome-icon :icon="['fas', 'sync']" class="me-1" />
                        Bump
                      </button>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Edit modal. Same shape as TemplatesView's create modal so
         operators see one consistent form. Description-only edits land
         in place; permission edits create a new template_versions row
         and bump latest_version — pinned SSKs are NOT auto-rolled. -->
    <div v-if="showEditModal" class="modal show d-block" tabindex="-1" style="background: rgba(0,0,0,0.5)">
      <div class="modal-dialog modal-lg">
        <div class="modal-content">
          <div class="modal-header">
            <h5 class="modal-title">
              Edit template "{{ template.name }}"
              <small class="text-muted">— editing permissions creates v{{ template.latestVersion + 1 }}</small>
            </h5>
            <button type="button" class="btn-close" @click="closeEditModal"></button>
          </div>
          <div class="modal-body">
            <div v-if="editFormError" class="alert alert-danger">{{ editFormError }}</div>
            <div class="alert alert-warning small mb-3">
              Pinned SSKs are NOT automatically rolled to the new version.
              After saving, use the "Dependent scoped signing keys" table
              below to bump each SSK explicitly (or leave them on the
              current version).
            </div>
            <form @submit.prevent="handleEditSubmit">
              <div class="mb-3">
                <label class="form-label">Description</label>
                <input v-model="editFormData.description" type="text" class="form-control" />
              </div>
              <div class="row">
                <div class="col-md-6 mb-3">
                  <label class="form-label">Pub Allow</label>
                  <textarea v-model="editPubAllowText" class="form-control font-monospace" rows="3" :placeholder="`events.>\n_INBOX.>`"></textarea>
                </div>
                <div class="col-md-6 mb-3">
                  <label class="form-label">Pub Deny</label>
                  <textarea v-model="editPubDenyText" class="form-control font-monospace" rows="3" placeholder="admin.>"></textarea>
                </div>
                <div class="col-md-6 mb-3">
                  <label class="form-label">Sub Allow</label>
                  <textarea v-model="editSubAllowText" class="form-control font-monospace" rows="3" :placeholder="`events.>\nmetrics.>`"></textarea>
                </div>
                <div class="col-md-6 mb-3">
                  <label class="form-label">Sub Deny</label>
                  <textarea v-model="editSubDenyText" class="form-control font-monospace" rows="3"></textarea>
                </div>
              </div>
              <div class="d-flex justify-content-end mb-2">
                <div class="form-check form-switch m-0">
                  <input class="form-check-input" type="checkbox" id="tplEditShowAdvanced" v-model="editShowAdvanced">
                  <label class="form-check-label small" for="tplEditShowAdvanced">Show advanced</label>
                </div>
              </div>

              <div v-if="editShowAdvanced">
                <div class="alert alert-info small">
                  <strong>Response permissions.</strong> NATS auto-grants a
                  narrow publish on a request's reply inbox
                  (<code>_INBOX.&lt;random&gt;</code>) so responders don't need
                  blanket <code>pub_allow: _INBOX.&gt;</code>. Max msgs caps replies
                  per request; TTL caps how long the grant lasts after the
                  request arrives. NATS does <em>not</em> auto-extend the TTL
                  — streaming responses that run past it hit a permission
                  violation. Leave both at <code>0</code> to get NATS's
                  server-side defaults (<code>1 msg / 2 min</code>);
                  auto-grant only activates on SSKs with a restricted
                  <code>pub_allow</code>.
                </div>
                <div class="row">
                  <div class="col-md-6 mb-3">
                    <label class="form-label">Response Max Msgs</label>
                    <input v-model.number="editFormData.responseMaxMsgs" type="number" min="0" class="form-control" />
                    <div class="form-text small">Replies allowed per request. <code>0</code> ⇒ NATS default (1).</div>
                  </div>
                  <div class="col-md-6 mb-3">
                    <label class="form-label">Response TTL (seconds)</label>
                    <input v-model.number="editFormData.responseTTLSeconds" type="number" min="0" class="form-control" />
                    <div class="form-text small">Window the auto-grant stays valid. <code>0</code> ⇒ NATS default (2 min).</div>
                  </div>
                </div>
                <div class="mb-0">
                  <label class="form-label">Change note</label>
                  <input v-model="editFormData.changeNote" type="text" class="form-control" placeholder="describe what changed" />
                  <div class="form-text small">Recorded on the new version row. Ignored on description-only edits.</div>
                </div>
              </div>
              <div class="text-end">
                <button type="button" class="btn btn-secondary me-2" @click="closeEditModal">Cancel</button>
                <button type="submit" class="btn btn-primary" :disabled="editSaving">
                  {{ editSaving ? 'Saving...' : 'Save' }}
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
import { ref, onMounted, h } from 'vue'
import { useRoute } from 'vue-router'
import apiClient from '@/utils/api'

const PermList = (props) => {
  const list = props.subjects || []
  if (list.length === 0) {
    return h('div', { class: 'text-muted small' }, '(none)')
  }
  return h('ul', { class: 'list-unstyled mb-0' },
    list.map((s) => h('li', null, [h('code', null, s)]))
  )
}
PermList.props = ['subjects']

const route = useRoute()
const template = ref(null)
const currentVersion = ref(null)
const versions = ref([])
const dependents = ref([])
const loading = ref(false)
const error = ref('')
const bumpingId = ref('')

const showEditModal = ref(false)
const editSaving = ref(false)
const editFormError = ref('')
const editShowAdvanced = ref(false)
const editFormData = ref({})
const editPubAllowText = ref('')
const editPubDenyText = ref('')
const editSubAllowText = ref('')
const editSubDenyText = ref('')

const openEditModal = () => {
  // Prepopulate with the current version's snapshot so the operator can
  // see what they're starting from. Permissions live on the version,
  // description on the parent template row.
  editFormData.value = {
    description: template.value?.description || '',
    // Preserve whatever the current version has; fall back to 0 (no
    // cap) when unset, matching the create-form default.
    responseMaxMsgs: currentVersion.value?.responsePermission?.maxMsgs ?? 0,
    // Proto stores ns; convert to seconds for the form. The submit
    // handler converts back.
    responseTTLSeconds: Math.round((currentVersion.value?.responsePermission?.expires || 0) / 1_000_000_000),
    changeNote: ''
  }
  editPubAllowText.value = (currentVersion.value?.permissions?.pubAllow || []).join('\n')
  editPubDenyText.value = (currentVersion.value?.permissions?.pubDeny || []).join('\n')
  editSubAllowText.value = (currentVersion.value?.permissions?.subAllow || []).join('\n')
  editSubDenyText.value = (currentVersion.value?.permissions?.subDeny || []).join('\n')
  editFormError.value = ''
  editShowAdvanced.value = false
  showEditModal.value = true
}

const closeEditModal = () => {
  showEditModal.value = false
  editFormError.value = ''
}

const trimSplit = (s) => (s || '').split('\n').map(x => x.trim()).filter(x => x !== '')

const handleEditSubmit = async () => {
  editSaving.value = true
  editFormError.value = ''
  try {
    await apiClient.post('/nis.v1.TemplateService/UpdateTemplate', {
      id: template.value.id,
      description: editFormData.value.description,
      permissions: {
        pubAllow: trimSplit(editPubAllowText.value),
        pubDeny: trimSplit(editPubDenyText.value),
        subAllow: trimSplit(editSubAllowText.value),
        subDeny: trimSplit(editSubDenyText.value)
      },
      responsePermission: {
        maxMsgs: editFormData.value.responseMaxMsgs || 0,
        expires: (editFormData.value.responseTTLSeconds || 0) * 1_000_000_000
      },
      changeNote: editFormData.value.changeNote
    })
    closeEditModal()
    await load()
  } catch (err) {
    editFormError.value = err.response?.data?.message || 'Failed to update template'
  } finally {
    editSaving.value = false
  }
}

const load = async () => {
  loading.value = true
  error.value = ''
  try {
    const gt = await apiClient.post('/nis.v1.TemplateService/GetTemplate', { id: route.params.id })
    template.value = gt.data.template
    currentVersion.value = gt.data.version

    const lv = await apiClient.post('/nis.v1.TemplateService/ListTemplateVersions', { templateId: route.params.id })
    versions.value = lv.data.versions || []

    const ld = await apiClient.post('/nis.v1.TemplateService/ListTemplateDependents', { templateId: route.params.id })
    dependents.value = ld.data.dependents || []
  } catch (err) {
    error.value = err.response?.data?.message || 'Failed to load template'
  } finally {
    loading.value = false
  }
}

const handleBump = async (d) => {
  const msg = d.drifted
    ? `Bump "${d.scopedSigningKeyName}" to v${template.value.latestVersion}?\n\nThis SSK was edited directly since its last bump — those edits will be OVERWRITTEN by the template's v${template.value.latestVersion} permissions.`
    : `Apply template v${template.value.latestVersion} to "${d.scopedSigningKeyName}"?\n\nThe SSK's permissions will be snapshotted from the template version and pushed to every attached cluster.`
  if (!confirm(msg)) return
  bumpingId.value = d.scopedSigningKeyId
  try {
    await apiClient.post('/nis.v1.TemplateService/ApplyTemplateToScopedKey', {
      scopedSigningKeyId: d.scopedSigningKeyId,
      versionNumber: template.value.latestVersion
    })
    await load()
  } catch (err) {
    error.value = err.response?.data?.message || 'Failed to bump SSK'
  } finally {
    bumpingId.value = ''
  }
}

const formatDate = (dateStr) => {
  if (!dateStr) return '-'
  return new Date(dateStr).toLocaleString()
}

onMounted(load)
</script>

<style scoped>
dt { font-weight: 600; }
dd { margin-bottom: 0.5rem; }
</style>
