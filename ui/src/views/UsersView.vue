<template>
  <div class="container-fluid py-4">
    <div class="row mb-3">
      <div class="col-md-4">
        <label for="operatorFilter" class="form-label">Filter by Operator</label>
        <select
          id="operatorFilter"
          v-model="selectedOperatorFilter"
          class="form-select"
          @change="selectedAccountFilter = ''"
        >
          <option value="">All Operators</option>
          <option v-for="op in operators" :key="op.id" :value="op.id">
            {{ op.name }}
          </option>
        </select>
      </div>
      <div class="col-md-4">
        <label for="accountFilter" class="form-label">Filter by Account</label>
        <select
          id="accountFilter"
          v-model="selectedAccountFilter"
          class="form-select"
          :disabled="!selectedOperatorFilter"
        >
          <option value="">All Accounts</option>
          <option v-for="acc in filteredAccountsForFilter" :key="acc.id" :value="acc.id">
            {{ acc.name }}
          </option>
        </select>
      </div>
    </div>

    <EntityList
      title="Users"
      entity-name="User"
      :items="filteredUsers"
      :columns="columns"
      :loading="loading"
      :error="error"
      @create="showCreateModal"
      @edit="showEditModal"
      @delete="handleDelete"
      @select="handleSelect"
    >
      <template #cell-name="{ item }">
        {{ item.name }}
        <span v-if="isSystemUser(item)" class="badge bg-info ms-2" title="System User">
          <font-awesome-icon :icon="['fas', 'shield-alt']" class="me-1" />
          System
        </span>
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

      <template #custom-actions="{ item }">
        <button
          v-if="isSystemUser(item)"
          class="btn btn-outline-danger"
          disabled
          title="Cannot delete system user"
        >
          <font-awesome-icon :icon="['fas', 'trash']" />
        </button>
      </template>
    </EntityList>

    <EntityForm
      v-if="showModal"
      :title="editingUser ? 'Edit User' : 'Create User'"
      :submit-label="editingUser ? 'Update' : 'Create'"
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
            :disabled="editingUser"
            @change="() => { localFormData.accountId = ''; scopedKeys = [] }"
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
            :disabled="editingUser || !localFormData.operatorId"
            @change="() => loadScopedKeys(localFormData.accountId)"
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
            placeholder="my-user"
            required
            :disabled="editingUser"
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
          <label for="scopedSigningKeyId" class="form-label">Scoped Signing Key</label>
          <select
            id="scopedSigningKeyId"
            v-model="localFormData.scopedSigningKeyId"
            class="form-select"
            :disabled="!localFormData.accountId"
          >
            <option value="">None</option>
            <option v-for="key in scopedKeys" :key="key.id" :value="key.id">
              {{ key.name }} ({{ key.role }})
            </option>
          </select>
          <div class="form-text">Optional scoped signing key for permission delegation (select an account first)</div>
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
const users = ref([])
const accounts = ref([])
const operators = ref([])
const scopedKeys = ref([])
const loading = ref(false)
const error = ref('')
const showModal = ref(false)
const editingUser = ref(null)
const formData = ref({})
const saving = ref(false)
const formError = ref('')
const selectedOperatorFilter = ref('')
const selectedAccountFilter = ref('')

const columns = [
  { key: 'name', label: 'Name' },
  { key: 'account', label: 'Account' },
  { key: 'operator', label: 'Operator' },
  { key: 'description', label: 'Description' },
  { key: 'publicKey', label: 'Public Key' },
  { key: 'createdAt', label: 'Created' }
]

const filteredAccountsForFilter = computed(() => {
  if (!selectedOperatorFilter.value) {
    return accounts.value
  }
  return accounts.value.filter(account => account.operatorId === selectedOperatorFilter.value)
})

const filteredUsers = computed(() => {
  let result = users.value

  // Filter by account (which implicitly filters by operator too)
  if (selectedAccountFilter.value) {
    result = result.filter(user => user.accountId === selectedAccountFilter.value)
  }
  // Filter by operator (if no account filter is set)
  else if (selectedOperatorFilter.value) {
    const operatorAccountIds = accounts.value
      .filter(account => account.operatorId === selectedOperatorFilter.value)
      .map(account => account.id)
    result = result.filter(user => operatorAccountIds.includes(user.accountId))
  }

  return result
})

const loadUsers = async () => {
  loading.value = true
  error.value = ''
  try {
    const response = await apiClient.post('/nis.v1.UserService/ListUsers', {})
    users.value = response.data.users || []
  } catch (err) {
    error.value = err.response?.data?.message || 'Failed to load users'
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

const isSystemUser = (user) => {
  const account = accounts.value.find(a => a.id === user.accountId)
  return account && account.name === '$SYS' && user.name === 'system'
}

const loadScopedKeys = async (accountId) => {
  if (!accountId) {
    scopedKeys.value = []
    return
  }

  try {
    const response = await apiClient.post('/nis.v1.ScopedSigningKeyService/ListScopedSigningKeys', {
      accountId
    })
    scopedKeys.value = response.data.keys || []
  } catch (err) {
    console.error('Failed to load scoped keys:', err)
    scopedKeys.value = []
  }
}

const showCreateModal = () => {
  editingUser.value = null
  formData.value = {
    name: '',
    description: '',
    operatorId: '',
    accountId: '',
    scopedSigningKeyId: ''
  }
  showModal.value = true
  formError.value = ''
}

const showEditModal = (user) => {
  editingUser.value = user
  const account = accounts.value.find(a => a.id === user.accountId)
  formData.value = {
    ...user,
    operatorId: account ? account.operatorId : ''
  }
  showModal.value = true
  formError.value = ''
}

const closeModal = () => {
  showModal.value = false
  editingUser.value = null
  formData.value = {}
  formError.value = ''
  scopedKeys.value = []
}

const handleSubmit = async (data) => {
  saving.value = true
  formError.value = ''
  try {
    if (editingUser.value) {
      await apiClient.post('/nis.v1.UserService/UpdateUser', {
        id: editingUser.value.id,
        ...data
      })
    } else {
      await apiClient.post('/nis.v1.UserService/CreateUser', data)
    }
    closeModal()
    await loadUsers()
  } catch (err) {
    formError.value = err.response?.data?.message || 'Failed to save user'
  } finally {
    saving.value = false
  }
}

const handleDelete = async (user) => {
  if (!confirm(`Are you sure you want to delete user "${user.name}"?`)) {
    return
  }

  try {
    await apiClient.post('/nis.v1.UserService/DeleteUser', {
      id: user.id
    })
    await loadUsers()
  } catch (err) {
    error.value = err.response?.data?.message || 'Failed to delete user'
  }
}

const handleSelect = (user) => {
  router.push(`/users/${user.id}`)
}

const formatDate = (dateStr) => {
  if (!dateStr) return '-'
  return new Date(dateStr).toLocaleString()
}

onMounted(() => {
  loadUsers()
  loadAccounts()
  loadOperators()
})
</script>
