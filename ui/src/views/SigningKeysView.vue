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
      <template #cell-account="{ item }">
        {{ getAccountName(item.accountId) }}
      </template>

      <template #cell-operator="{ item }">
        {{ getOperatorName(item.accountId) }}
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

        <div class="mb-3">
          <label for="pubAllow" class="form-label">Publish Allow</label>
          <textarea
            id="pubAllow"
            v-model="pubAllowText"
            class="form-control"
            rows="2"
            placeholder="events.>\ndata.>"
          ></textarea>
          <div class="form-text">One subject per line. Use '>' for wildcards.</div>
        </div>

        <div class="mb-3">
          <label for="pubDeny" class="form-label">Publish Deny</label>
          <textarea
            id="pubDeny"
            v-model="pubDenyText"
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
            v-model="subAllowText"
            class="form-control"
            rows="2"
            placeholder="events.>\nresponses.>"
          ></textarea>
          <div class="form-text">One subject per line.</div>
        </div>

        <div class="mb-3">
          <label for="subDeny" class="form-label">Subscribe Deny</label>
          <textarea
            id="subDeny"
            v-model="subDenyText"
            class="form-control"
            rows="2"
          ></textarea>
          <div class="form-text">One subject per line.</div>
        </div>
      </template>
    </EntityForm>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import apiClient from '@/utils/api'
import EntityList from '@/components/EntityList.vue'
import EntityForm from '@/components/EntityForm.vue'

const router = useRouter()
const signingKeys = ref([])
const accounts = ref([])
const operators = ref([])
const loading = ref(false)
const error = ref('')
const showModal = ref(false)
const editingKey = ref(null)
const formData = ref({})
const saving = ref(false)
const formError = ref('')

const pubAllowText = computed({
  get() {
    return formData.value.pubAllow?.join('\n') || ''
  },
  set(value) {
    formData.value.pubAllow = value.split('\n').filter(s => s.trim() !== '')
  }
})

const pubDenyText = computed({
  get() {
    return formData.value.pubDeny?.join('\n') || ''
  },
  set(value) {
    formData.value.pubDeny = value.split('\n').filter(s => s.trim() !== '')
  }
})

const subAllowText = computed({
  get() {
    return formData.value.subAllow?.join('\n') || ''
  },
  set(value) {
    formData.value.subAllow = value.split('\n').filter(s => s.trim() !== '')
  }
})

const subDenyText = computed({
  get() {
    return formData.value.subDeny?.join('\n') || ''
  },
  set(value) {
    formData.value.subDeny = value.split('\n').filter(s => s.trim() !== '')
  }
})

const columns = [
  { key: 'name', label: 'Name' },
  { key: 'account', label: 'Account' },
  { key: 'operator', label: 'Operator' },
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
  } catch (err) {
    console.error('Failed to load operators:', err)
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
    subDeny: []
  }
  showModal.value = true
  formError.value = ''
}

const showEditModal = (key) => {
  editingKey.value = key
  const account = accounts.value.find(a => a.id === key.accountId)
  formData.value = {
    ...key,
    operatorId: account ? account.operatorId : ''
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
  try {
    if (editingKey.value) {
      await apiClient.post('/nis.v1.ScopedSigningKeyService/UpdateScopedSigningKey', {
        id: editingKey.value.id,
        name: data.name,
        description: data.description
      })
    } else {
      await apiClient.post('/nis.v1.ScopedSigningKeyService/CreateScopedSigningKey', {
        accountId: data.accountId,
        name: data.name,
        description: data.description,
        permissions: {
          pubAllow: data.pubAllow || [],
          pubDeny: data.pubDeny || [],
          subAllow: data.subAllow || [],
          subDeny: data.subDeny || []
        }
      })
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
  // Navigate to detail view if implemented
  // router.push(`/signing-keys/${key.id}`)
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
