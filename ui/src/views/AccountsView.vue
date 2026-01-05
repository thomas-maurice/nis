<template>
  <div class="container-fluid py-4">
    <div class="row mb-3">
      <div class="col-md-4">
        <label for="operatorFilter" class="form-label">Filter by Operator</label>
        <select
          id="operatorFilter"
          v-model="selectedOperatorFilter"
          class="form-select"
        >
          <option value="">All Operators</option>
          <option v-for="op in operators" :key="op.id" :value="op.id">
            {{ op.name }}
          </option>
        </select>
      </div>
    </div>

    <EntityList
      title="Accounts"
      entity-name="Account"
      :items="filteredAccounts"
      :columns="columns"
      :loading="loading"
      :error="error"
      @create="showCreateModal"
      @edit="showEditModal"
      @delete="handleDelete"
      @select="handleSelect"
    >
      <template #cell-operator="{ item }">
        {{ getOperatorName(item.operatorId) }}
      </template>

      <template #cell-name="{ item }">
        {{ item.name }}
        <span v-if="isSystemAccount(item)" class="badge bg-info ms-2" title="System Account">
          <font-awesome-icon :icon="['fas', 'shield-alt']" class="me-1" />
          System
        </span>
      </template>

      <template #cell-publicKey="{ item }">
        <ClickablePubKey :pubkey="item.publicKey" truncate />
      </template>

      <template #cell-jetstreamEnabled="{ item }">
        <span :class="item.jetstreamLimits?.enabled ? 'badge bg-success' : 'badge bg-secondary'">
          {{ item.jetstreamLimits?.enabled ? 'Enabled' : 'Disabled' }}
        </span>
      </template>

      <template #cell-createdAt="{ item }">
        {{ formatDate(item.createdAt) }}
      </template>

      <template #custom-actions="{ item }">
        <button
          v-if="isSystemAccount(item)"
          class="btn btn-outline-danger"
          disabled
          title="Cannot delete system account"
        >
          <font-awesome-icon :icon="['fas', 'trash']" />
        </button>
      </template>
    </EntityList>

    <EntityForm
      v-if="showModal"
      :title="editingAccount ? 'Edit Account' : 'Create Account'"
      :submit-label="editingAccount ? 'Update' : 'Create'"
      :initial-data="formData"
      :loading="saving"
      :error="formError"
      @submit="handleSubmit"
      @close="closeModal"
    >
      <template #fields="{ formData }">
        <div class="mb-3">
          <label for="operatorId" class="form-label">Operator <span class="text-danger">*</span></label>
          <select
            id="operatorId"
            v-model="formData.operatorId"
            class="form-select"
            required
            :disabled="editingAccount"
          >
            <option value="">Select operator...</option>
            <option v-for="op in operators" :key="op.id" :value="op.id">
              {{ op.name }}
            </option>
          </select>
        </div>

        <div class="mb-3">
          <label for="name" class="form-label">Name <span class="text-danger">*</span></label>
          <input
            id="name"
            v-model="formData.name"
            type="text"
            class="form-control"
            placeholder="my-account"
            required
            :disabled="editingAccount"
          />
        </div>

        <div class="mb-3">
          <label for="description" class="form-label">Description</label>
          <textarea
            id="description"
            v-model="formData.description"
            class="form-control"
            rows="2"
          ></textarea>
        </div>

        <div class="mb-3">
          <div class="form-check">
            <input
              id="jetstreamEnabled"
              v-model="formData.jetstreamEnabled"
              class="form-check-input"
              type="checkbox"
            />
            <label class="form-check-label" for="jetstreamEnabled">
              Enable JetStream
            </label>
          </div>
        </div>

        <div v-if="formData.jetstreamEnabled">
          <div class="mb-3">
            <label for="jetstreamMaxMemory" class="form-label">Max Memory (bytes)</label>
            <input
              id="jetstreamMaxMemory"
              v-model.number="formData.jetstreamMaxMemory"
              type="number"
              class="form-control"
              placeholder="-1 (unlimited)"
            />
          </div>

          <div class="mb-3">
            <label for="jetstreamMaxStorage" class="form-label">Max Storage (bytes)</label>
            <input
              id="jetstreamMaxStorage"
              v-model.number="formData.jetstreamMaxStorage"
              type="number"
              class="form-control"
              placeholder="-1 (unlimited)"
            />
          </div>

          <div class="mb-3">
            <label for="jetstreamMaxStreams" class="form-label">Max Streams</label>
            <input
              id="jetstreamMaxStreams"
              v-model.number="formData.jetstreamMaxStreams"
              type="number"
              class="form-control"
              placeholder="-1 (unlimited)"
            />
          </div>

          <div class="mb-3">
            <label for="jetstreamMaxConsumers" class="form-label">Max Consumers</label>
            <input
              id="jetstreamMaxConsumers"
              v-model.number="formData.jetstreamMaxConsumers"
              type="number"
              class="form-control"
              placeholder="-1 (unlimited)"
            />
          </div>
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
import ClickablePubKey from '@/components/ClickablePubKey.vue'

const router = useRouter()
const accounts = ref([])
const operators = ref([])
const loading = ref(false)
const error = ref('')
const showModal = ref(false)
const editingAccount = ref(null)
const formData = ref({})
const saving = ref(false)
const formError = ref('')
const selectedOperatorFilter = ref('')

const columns = [
  { key: 'name', label: 'Name' },
  { key: 'operator', label: 'Operator' },
  { key: 'description', label: 'Description' },
  { key: 'publicKey', label: 'Public Key' },
  { key: 'jetstreamEnabled', label: 'JetStream' },
  { key: 'createdAt', label: 'Created' }
]

const filteredAccounts = computed(() => {
  if (!selectedOperatorFilter.value) {
    return accounts.value
  }
  return accounts.value.filter(account => account.operatorId === selectedOperatorFilter.value)
})

const loadAccounts = async () => {
  loading.value = true
  error.value = ''
  try {
    const response = await apiClient.post('/nis.v1.AccountService/ListAccounts', {})
    accounts.value = response.data.accounts || []
  } catch (err) {
    error.value = err.response?.data?.message || 'Failed to load accounts'
  } finally {
    loading.value = false
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

const getOperatorName = (operatorId) => {
  const operator = operators.value.find(o => o.id === operatorId)
  return operator ? operator.name : '-'
}

const isSystemAccount = (account) => {
  const operator = operators.value.find(o => o.id === account.operatorId)
  return operator && operator.systemAccountPubKey === account.publicKey
}

const showCreateModal = () => {
  editingAccount.value = null
  formData.value = {
    name: '',
    description: '',
    operatorId: '',
    jetstreamEnabled: true,
    jetstreamMaxMemory: -1,
    jetstreamMaxStorage: -1,
    jetstreamMaxStreams: -1,
    jetstreamMaxConsumers: -1
  }
  showModal.value = true
  formError.value = ''
}

const showEditModal = (account) => {
  editingAccount.value = account
  formData.value = {
    ...account,
    jetstreamEnabled: account.jetstreamLimits?.enabled || false,
    jetstreamMaxMemory: account.jetstreamLimits?.maxMemory || -1,
    jetstreamMaxStorage: account.jetstreamLimits?.maxStorage || -1,
    jetstreamMaxStreams: account.jetstreamLimits?.maxStreams || -1,
    jetstreamMaxConsumers: account.jetstreamLimits?.maxConsumers || -1
  }
  showModal.value = true
  formError.value = ''
}

const closeModal = () => {
  showModal.value = false
  editingAccount.value = null
  formData.value = {}
  formError.value = ''
}

const handleSubmit = async (data) => {
  saving.value = true
  formError.value = ''
  try {
    if (editingAccount.value) {
      // Update basic account info (name and description)
      await apiClient.post('/nis.v1.AccountService/UpdateAccount', {
        id: editingAccount.value.id,
        name: data.name,
        description: data.description
      })

      // Update JetStream limits separately
      await apiClient.post('/nis.v1.AccountService/UpdateJetStreamLimits', {
        id: editingAccount.value.id,
        limits: {
          enabled: data.jetstreamEnabled,
          maxMemory: data.jetstreamMaxMemory || -1,
          maxStorage: data.jetstreamMaxStorage || -1,
          maxStreams: data.jetstreamMaxStreams || -1,
          maxConsumers: data.jetstreamMaxConsumers || -1
        }
      })
    } else {
      // Create new account with JetStream limits
      await apiClient.post('/nis.v1.AccountService/CreateAccount', {
        operatorId: data.operatorId,
        name: data.name,
        description: data.description,
        jetstreamLimits: {
          enabled: data.jetstreamEnabled,
          maxMemory: data.jetstreamMaxMemory || -1,
          maxStorage: data.jetstreamMaxStorage || -1,
          maxStreams: data.jetstreamMaxStreams || -1,
          maxConsumers: data.jetstreamMaxConsumers || -1
        }
      })
    }
    closeModal()
    await loadAccounts()
  } catch (err) {
    formError.value = err.response?.data?.message || 'Failed to save account'
  } finally {
    saving.value = false
  }
}

const handleDelete = async (account) => {
  if (!confirm(`Are you sure you want to delete account "${account.name}"?`)) {
    return
  }

  try {
    await apiClient.post('/nis.v1.AccountService/DeleteAccount', {
      id: account.id
    })
    await loadAccounts()
  } catch (err) {
    error.value = err.response?.data?.message || 'Failed to delete account'
  }
}

const handleSelect = (account) => {
  router.push(`/accounts/${account.id}`)
}

const formatDate = (dateStr) => {
  if (!dateStr) return '-'
  return new Date(dateStr).toLocaleString()
}

onMounted(() => {
  loadAccounts()
  loadOperators()
})
</script>
