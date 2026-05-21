<template>
  <div class="container-fluid py-4">
    <EntityList
      title="Scoped Signing Keys"
      entity-name="Signing Key"
      :items="signingKeys"
      :columns="columns"
      :loading="loading"
      :error="error"
      @create="showCreateModal"
      @edit="showEditModal"
      @delete="handleDelete"
      @select="handleSelect"
    >
      <template #custom-actions="{ item }">
        <button
          class="btn btn-outline-secondary"
          @click="handleSelect(item)"
          title="View"
        >
          <font-awesome-icon :icon="['fas', 'eye']" />
        </button>
      </template>

      <template #cell-account="{ item }">
        {{ getAccountName(item.accountId) }}
      </template>

      <template #cell-operator="{ item }">
        {{ getOperatorName(item.accountId) }}
      </template>

      <template #cell-publicKey="{ item }">
        <ClickablePubKey :pubkey="item.publicKey" truncate />
      </template>

      <template #cell-createdAt="{ item }">
        {{ formatDate(item.createdAt) }}
      </template>
    </EntityList>

    <EntityForm
      v-if="showModal"
      :title="editingKey ? 'Edit Signing Key' : 'Create Signing Key'"
      :submit-label="editingKey ? 'Update' : 'Create'"
      :initial-data="formData"
      :loading="saving"
      :error="formError"
      @submit="handleSubmit"
      @close="closeModal"
    >
      <template #fields="{ formData: localFormData }">
        <div class="mb-3">
          <label for="operatorId" class="form-label">Operator <span class="text-danger">*</span></label>
          <select
            id="operatorId"
            v-model="localFormData.operatorId"
            class="form-select"
            required
            :disabled="editingKey"
            @change="() => { localFormData.accountId = '' }"
          >
            <option value="">Select operator...</option>
            <option v-for="op in operators" :key="op.id" :value="op.id">
              {{ op.name }}
            </option>
          </select>
        </div>

        <div class="mb-3">
          <label for="accountId" class="form-label">Account <span class="text-danger">*</span></label>
          <select
            id="accountId"
            v-model="localFormData.accountId"
            class="form-select"
            required
            :disabled="editingKey || !localFormData.operatorId"
          >
            <option value="">Select account...</option>
            <option v-for="acc in accounts.filter(a => a.operatorId === localFormData.operatorId)" :key="acc.id" :value="acc.id">
              {{ acc.name }}
            </option>
          </select>
        </div>

        <div class="mb-3">
          <label for="name" class="form-label">Name <span class="text-danger">*</span></label>
          <input
            id="name"
            v-model="localFormData.name"
            type="text"
            class="form-control"
            placeholder="readonly-key"
            required
            :disabled="editingKey"
          />
        </div>

        <div class="mb-3">
          <label for="description" class="form-label">Description</label>
          <textarea
            id="description"
            v-model="localFormData.description"
            class="form-control"
            rows="2"
          ></textarea>
        </div>

        <!-- Template binding (P6). Form-switch on the right matches the
             cluster-drift toggle pattern; no background colour so it
             inherits the surrounding theme (light/dark). Selecting a
             template HIDES the manual permission fields entirely — the
             template snapshot is what gets applied, so showing greyed-
             out fields was just noise. -->
        <div v-if="!editingKey && allTemplates.length > 0" class="mb-3">
          <div class="d-flex justify-content-between align-items-center mb-2">
            <label class="form-label mb-0"><strong>Create from template</strong></label>
            <div class="form-check form-switch m-0">
              <input
                id="useTemplate"
                type="checkbox"
                class="form-check-input"
                v-model="localFormData.useTemplate"
                :disabled="!localFormData.operatorId"
                @change="onUseTemplateToggle(localFormData)"
              >
              <label class="form-check-label small" for="useTemplate">Use template</label>
            </div>
          </div>
          <div v-if="localFormData.useTemplate">
            <div class="form-text mb-2">
              The SSK is pinned to a template version and its permissions are
              a snapshot of the template. Future template updates do NOT
              auto-roll; bump explicitly from the template detail page.
            </div>
            <div class="row">
              <div class="col-md-7 mb-2">
                <label for="templateName" class="form-label">Template <span class="text-danger">*</span></label>
                <select
                  id="templateName"
                  v-model="localFormData.templateName"
                  class="form-select"
                  :required="localFormData.useTemplate"
                  @change="onTemplateSelect(localFormData)"
                >
                  <option value="">Select template...</option>
                  <option
                    v-for="t in allTemplates.filter(x => x.operatorId === localFormData.operatorId)"
                    :key="t.id"
                    :value="t.name"
                  >
                    {{ t.name }} (current: v{{ t.latestVersion }}){{ t.description ? ' — ' + t.description : '' }}
                  </option>
                </select>
              </div>
              <div class="col-md-3 mb-2">
                <label for="templateVersion" class="form-label">Pin version</label>
                <input
                  id="templateVersion"
                  type="number"
                  min="1"
                  v-model.number="localFormData.templateVersion"
                  class="form-control"
                  :disabled="localFormData.trackLatest"
                />
                <div class="form-text small">
                  <span v-if="localFormData.trackLatest">Resolved server-side at create.</span>
                  <span v-else>SSK stays on this version until you bump it.</span>
                </div>
              </div>
              <div class="col-md-2 mb-2">
                <label class="form-label">&nbsp;</label>
                <div class="form-check form-switch mt-2">
                  <input
                    id="trackLatest"
                    type="checkbox"
                    class="form-check-input"
                    v-model="localFormData.trackLatest"
                    @change="onTrackLatestToggle(localFormData)"
                  >
                  <label class="form-check-label small" for="trackLatest">Track latest</label>
                </div>
              </div>
            </div>
            <div v-if="localFormData.trackLatest" class="alert alert-warning small mt-2 mb-0">
              <strong>Track latest</strong> auto-applies every new template
              version to this SSK: the parent account JWT is re-signed and
              pushed to all clusters whenever the template is updated. Direct
              permission edits to the SSK are rejected while this is on, so
              the next auto-bump can't silently overwrite them. Turn it off
              if you want the SSK pinned to a specific version until you
              bump it explicitly.
            </div>
          </div>
        </div>

        <!-- Manual permission fields. Hidden entirely when a template is
             selected (template wins; showing greyed-out fields just
             clutters the form). -->
        <template v-if="!localFormData.useTemplate">
          <div class="mb-3">
            <label for="pubAllow" class="form-label">Publish Allow</label>
            <textarea
              id="pubAllow"
              :value="(localFormData.pubAllow || []).join('\n')"
              @input="localFormData.pubAllow = $event.target.value.split('\n')"
              class="form-control"
              rows="2"
              :placeholder="`events.>\ndata.>`"
            ></textarea>
            <div class="form-text">One subject per line. Use '>' for wildcards.</div>
          </div>

          <div class="mb-3">
            <label for="pubDeny" class="form-label">Publish Deny</label>
            <textarea
              id="pubDeny"
              :value="(localFormData.pubDeny || []).join('\n')"
              @input="localFormData.pubDeny = $event.target.value.split('\n')"
              class="form-control"
              rows="2"
              placeholder="_INBOX.>"
            ></textarea>
            <div class="form-text">One subject per line.</div>
          </div>

          <div class="mb-3">
            <label for="subAllow" class="form-label">Subscribe Allow</label>
            <textarea
              id="subAllow"
              :value="(localFormData.subAllow || []).join('\n')"
              @input="localFormData.subAllow = $event.target.value.split('\n')"
              class="form-control"
              rows="2"
              :placeholder="`events.>\nresponses.>`"
            ></textarea>
            <div class="form-text">One subject per line.</div>
          </div>

          <div class="mb-3">
            <label for="subDeny" class="form-label">Subscribe Deny</label>
            <textarea
              id="subDeny"
              :value="(localFormData.subDeny || []).join('\n')"
              @input="localFormData.subDeny = $event.target.value.split('\n')"
              class="form-control"
              rows="2"
            ></textarea>
            <div class="form-text">One subject per line.</div>
          </div>
        </template>
      </template>
    </EntityForm>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import apiClient from '@/utils/api'
import EntityList from '@/components/EntityList.vue'
import EntityForm from '@/components/EntityForm.vue'
import ClickablePubKey from '@/components/ClickablePubKey.vue'

const router = useRouter()
const signingKeys = ref([])
const accounts = ref([])
const operators = ref([])
const allTemplates = ref([])
const loading = ref(false)
const error = ref('')
const showModal = ref(false)
const editingKey = ref(null)
const formData = ref({})
const saving = ref(false)
const formError = ref('')

// Textareas bind directly to `localFormData.pub*` / `localFormData.sub*` in
// the EntityForm slot so typing stays inside EntityForm's reactive scope.
// Routing through a parent computed (which would mutate the outer formData)
// trips EntityForm's deep watcher on initialData, which re-spreads the prop
// over its internal state and wipes the operator/account dropdown selections.

const columns = [
  { key: 'name', label: 'Name' },
  { key: 'account', label: 'Account' },
  { key: 'operator', label: 'Operator' },
  { key: 'publicKey', label: 'Public Key' },
  { key: 'description', label: 'Description' },
  { key: 'createdAt', label: 'Created' }
]

const loadSigningKeys = async () => {
  loading.value = true
  error.value = ''
  try {
    const response = await apiClient.post('/nis.v1.ScopedSigningKeyService/ListScopedSigningKeys', {})
    signingKeys.value = response.data.keys || []
  } catch (err) {
    error.value = err.response?.data?.message || 'Failed to load signing keys'
  } finally {
    loading.value = false
  }
}

const loadAccounts = async () => {
  try {
    const response = await apiClient.post('/nis.v1.AccountService/ListAccounts', {})
    accounts.value = response.data.accounts || []
  } catch (err) {
    console.error('Failed to load accounts:', err)
  }
}

const loadOperators = async () => {
  try {
    const response = await apiClient.post('/nis.v1.OperatorService/ListOperators', {})
    operators.value = response.data.operators || []
    // Once we know the operator list, fan out a ListTemplates per operator
    // so the SSK create form can offer a "Create from template" selector.
    // For typical operator counts (<10) this is fine; if it ever grows we
    // can lazy-load on operator-dropdown change instead.
    await loadAllTemplates()
  } catch (err) {
    console.error('Failed to load operators:', err)
  }
}

const loadAllTemplates = async () => {
  try {
    const results = await Promise.all(
      operators.value.map((op) =>
        apiClient
          .post('/nis.v1.TemplateService/ListTemplates', { operatorId: op.id })
          .then((r) => (r.data.templates || []).map((t) => ({ ...t, operatorId: op.id })))
          .catch(() => [])
      )
    )
    allTemplates.value = results.flat()
  } catch (err) {
    console.error('Failed to load templates:', err)
  }
}

const getAccountName = (accountId) => {
  const account = accounts.value.find(a => a.id === accountId)
  return account ? account.name : '-'
}

const getOperatorName = (accountId) => {
  const account = accounts.value.find(a => a.id === accountId)
  if (!account) return '-'
  const operator = operators.value.find(o => o.id === account.operatorId)
  return operator ? operator.name : '-'
}

const showCreateModal = () => {
  editingKey.value = null
  formData.value = {
    name: '',
    description: '',
    operatorId: '',
    accountId: '',
    pubAllow: [],
    pubDeny: [],
    subAllow: [],
    subDeny: [],
    useTemplate: false,
    templateName: '',
    // Default is explicit pin to current latest (set when a template is
    // picked below). trackLatest=false makes the SSK stable across
    // future template bumps — matches the "no surprise cascade" rule.
    templateVersion: 0,
    trackLatest: false
  }
  showModal.value = true
  formError.value = ''
}

// Helpers exposed on the closure for the EntityForm slot to call. They
// keep template-related defaults sane as the user toggles things:
// - On useTemplate=true, prefill version to current latest (if a
//   template is already picked) so the operator sees what they're
//   committing to.
// - On template select, set version to that template's latestVersion.
// - On trackLatest toggle, clear or restore the explicit pin.
const onUseTemplateToggle = (data) => {
  if (!data.useTemplate) {
    data.templateName = ''
    data.templateVersion = 0
    data.trackLatest = false
    return
  }
  // If a template is already selected (e.g. user toggled off then on),
  // re-resolve to current latest.
  if (data.templateName) {
    const t = allTemplates.value.find(x => x.operatorId === data.operatorId && x.name === data.templateName)
    data.templateVersion = t ? t.latestVersion : 0
  }
}

const onTemplateSelect = (data) => {
  if (!data.templateName) return
  const t = allTemplates.value.find(x => x.operatorId === data.operatorId && x.name === data.templateName)
  if (t && !data.trackLatest) {
    data.templateVersion = t.latestVersion
  }
}

const onTrackLatestToggle = (data) => {
  if (data.trackLatest) {
    // Track-latest mode: server resolves to current latest at apply.
    // We zero the field so the API sees "no explicit pin" — service
    // layer treats 0 as "use latest".
    data.templateVersion = 0
  } else {
    // Back to explicit pin: prefill with the template's current latest
    // so the input isn't empty.
    const t = allTemplates.value.find(x => x.operatorId === data.operatorId && x.name === data.templateName)
    data.templateVersion = t ? t.latestVersion : 1
  }
}

const showEditModal = (key) => {
  editingKey.value = key
  const account = accounts.value.find(a => a.id === key.accountId)
  // Flatten the nested `permissions` object — the form's textarea bindings
  // read `formData.pubAllow` etc. directly, not `formData.permissions.*`.
  formData.value = {
    ...key,
    operatorId: account ? account.operatorId : '',
    pubAllow: key.permissions?.pubAllow ? [...key.permissions.pubAllow] : [],
    pubDeny: key.permissions?.pubDeny ? [...key.permissions.pubDeny] : [],
    subAllow: key.permissions?.subAllow ? [...key.permissions.subAllow] : [],
    subDeny: key.permissions?.subDeny ? [...key.permissions.subDeny] : []
  }
  showModal.value = true
  formError.value = ''
}

const closeModal = () => {
  showModal.value = false
  editingKey.value = null
  formData.value = {}
  formError.value = ''
}

const handleSubmit = async (data) => {
  saving.value = true
  formError.value = ''
  const trim = (arr) => (arr || []).map(s => s.trim()).filter(s => s !== '')
  try {
    if (editingKey.value) {
      // UpdateScopedSigningKey only accepts name/description per the proto.
      // Permission lists go through a separate UpdatePermissions RPC — without
      // this second call, edits silently drop pub/sub allow/deny changes.
      await apiClient.post('/nis.v1.ScopedSigningKeyService/UpdateScopedSigningKey', {
        id: editingKey.value.id,
        name: data.name,
        description: data.description
      })
      await apiClient.post('/nis.v1.ScopedSigningKeyService/UpdatePermissions', {
        id: editingKey.value.id,
        permissions: {
          pubAllow: trim(data.pubAllow),
          pubDeny: trim(data.pubDeny),
          subAllow: trim(data.subAllow),
          subDeny: trim(data.subDeny)
        }
      })
    } else {
      const createReq = {
        accountId: data.accountId,
        name: data.name,
        description: data.description,
        permissions: {
          pubAllow: trim(data.pubAllow),
          pubDeny: trim(data.pubDeny),
          subAllow: trim(data.subAllow),
          subDeny: trim(data.subDeny)
        }
      }
      // Template ref wins over the permission fields on create — see
      // service-side ScopedSigningKeyService.CreateScopedSigningKey,
      // which ignores Pub*/Sub* when TemplateRef is set. When
      // trackLatest is on we send 0 so the server resolves to current
      // latest; otherwise we send the explicit pin so the SSK stays
      // on that version until a manual bump.
      if (data.useTemplate && data.templateName) {
        createReq.template = {
          operatorId: data.operatorId,
          templateName: data.templateName,
          versionNumber: data.trackLatest ? 0 : (data.templateVersion || 0)
        }
        createReq.trackLatest = !!data.trackLatest
      }
      await apiClient.post('/nis.v1.ScopedSigningKeyService/CreateScopedSigningKey', createReq)
    }
    closeModal()
    await loadSigningKeys()
  } catch (err) {
    formError.value = err.response?.data?.message || 'Failed to save signing key'
  } finally {
    saving.value = false
  }
}

const handleDelete = async (key) => {
  if (!confirm(`Are you sure you want to delete signing key "${key.name}"?`)) {
    return
  }

  try {
    await apiClient.post('/nis.v1.ScopedSigningKeyService/DeleteScopedSigningKey', {
      id: key.id
    })
    await loadSigningKeys()
  } catch (err) {
    error.value = err.response?.data?.message || 'Failed to delete signing key'
  }
}

const handleSelect = (key) => {
  router.push(`/signing-keys/${key.id}`)
}

const formatDate = (dateStr) => {
  if (!dateStr) return '-'
  return new Date(dateStr).toLocaleString()
}

onMounted(() => {
  loadSigningKeys()
  loadAccounts()
  loadOperators()
})
</script>
