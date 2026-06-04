<template>
  <div class="container-fluid py-4">
    <div class="d-flex justify-content-between align-items-center mb-4">
      <h1>Local Users</h1>
      <button class="btn btn-primary" @click="showCreateModal = true">
        <font-awesome-icon :icon="['fas', 'plus']" class="me-2" />
        Create Local User
      </button>
    </div>

    <div v-if="loading" class="text-center py-5">
      <div class="spinner-border text-primary" role="status"></div>
    </div>

    <div v-else-if="error" class="alert alert-danger">{{ error }}</div>

    <div v-else class="card">
      <div class="card-body">
        <div class="table-responsive">
          <table class="table table-hover">
            <thead>
              <tr>
                <th>Username</th>
                <th>Role</th>
                <th>Organization</th>
                <th>Created</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="user in users" :key="user.id">
                <td>
                  <font-awesome-icon :icon="['fas', 'user']" class="me-2 text-muted" />
                  {{ user.username }}
                </td>
                <td>
                  <span :class="getRoleBadgeClass(user.permissions[0])">
                    {{ getRoleDisplay(user.permissions[0]) }}
                  </span>
                </td>
                <td>{{ orgName(user.organizationId) }}</td>
                <td>{{ formatDate(user.createdAt) }}</td>
                <td>
                  <button
                    class="btn btn-sm btn-outline-primary me-2"
                    @click="showChangePasswordModal(user)"
                    title="Change Password"
                  >
                    <font-awesome-icon :icon="['fas', 'key']" />
                  </button>
                  <button
                    class="btn btn-sm btn-outline-secondary me-2"
                    @click="showChangeRoleModal(user)"
                    title="Change Role"
                  >
                    <font-awesome-icon :icon="['fas', 'user-gear']" />
                  </button>
                  <button
                    class="btn btn-sm btn-outline-danger"
                    @click="confirmDelete(user)"
                    title="Delete User"
                  >
                    <font-awesome-icon :icon="['fas', 'trash']" />
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>

    <!-- Create User Modal -->
    <div v-if="showCreateModal" class="modal fade show d-block" tabindex="-1" style="background-color: rgba(0,0,0,0.5)">
      <div class="modal-dialog">
        <div class="modal-content">
          <div class="modal-header">
            <h5 class="modal-title">Create Local User</h5>
            <button type="button" class="btn-close" @click="closeCreateModal"></button>
          </div>
          <div class="modal-body">
            <div class="mb-3">
              <label for="username" class="form-label">Username</label>
              <input
                id="username"
                v-model="createForm.username"
                type="text"
                class="form-control"
                required
              />
            </div>
            <div class="mb-3">
              <label for="password" class="form-label">Password</label>
              <input
                id="password"
                v-model="createForm.password"
                type="password"
                class="form-control"
                required
              />
            </div>
            <div class="mb-3">
              <label for="role" class="form-label">Role</label>
              <select id="role" v-model="createForm.role" class="form-select" @change="handleRoleChange">
                <option value="admin">Admin (Full Access)</option>
                <option value="org-admin">Org Admin (Manage Organization)</option>
                <option value="operator-admin">Operator Admin (Manage Accounts/Users)</option>
                <option value="account-admin">Account Admin (Manage Users)</option>
              </select>
            </div>
            <div class="mb-3">
              <label for="organization" class="form-label">
                Organization
                <span v-if="createForm.role !== 'admin'" class="text-danger">*</span>
              </label>
              <select id="organization" v-model="createForm.organizationId" class="form-select">
                <option value="">{{ createForm.role === 'admin' ? 'None' : 'Select an organization...' }}</option>
                <option v-for="org in organizations" :key="org.id" :value="org.id">
                  {{ org.name }}
                </option>
              </select>
            </div>
            <div v-if="createForm.role === 'operator-admin'" class="mb-3">
              <label for="operator" class="form-label">Operator <span class="text-danger">*</span></label>
              <select id="operator" v-model="createForm.operatorId" class="form-select" required :disabled="!createForm.organizationId">
                <option value="">{{ createForm.organizationId ? 'Select an operator...' : 'Select an organization first...' }}</option>
                <option v-for="operator in filteredOperators" :key="operator.id" :value="operator.id">
                  {{ operator.name }}
                </option>
              </select>
            </div>
            <div v-if="createForm.role === 'account-admin'" class="mb-3">
              <label for="account" class="form-label">Account <span class="text-danger">*</span></label>
              <select id="account" v-model="createForm.accountId" class="form-select" required :disabled="!createForm.organizationId">
                <option value="">{{ createForm.organizationId ? 'Select an account...' : 'Select an organization first...' }}</option>
                <option v-for="account in filteredAccounts" :key="account.id" :value="account.id">
                  {{ account.name }} ({{ account.operatorName }})
                </option>
              </select>
            </div>
            <div v-if="createError" class="alert alert-danger">{{ createError }}</div>
          </div>
          <div class="modal-footer">
            <button type="button" class="btn btn-secondary" @click="closeCreateModal">Cancel</button>
            <button type="button" class="btn btn-primary" @click="handleCreate" :disabled="creating">
              <span v-if="creating" class="spinner-border spinner-border-sm me-2"></span>
              Create
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- Change Password Modal -->
    <div v-if="showPasswordModal" class="modal fade show d-block" tabindex="-1" style="background-color: rgba(0,0,0,0.5)">
      <div class="modal-dialog">
        <div class="modal-content">
          <div class="modal-header">
            <h5 class="modal-title">Change Password: {{ selectedUser?.username }}</h5>
            <button type="button" class="btn-close" @click="closePasswordModal"></button>
          </div>
          <div class="modal-body">
            <div class="mb-3">
              <label for="newPassword" class="form-label">New Password</label>
              <input
                id="newPassword"
                v-model="passwordForm.password"
                type="password"
                class="form-control"
                required
              />
            </div>
            <div v-if="passwordError" class="alert alert-danger">{{ passwordError }}</div>
          </div>
          <div class="modal-footer">
            <button type="button" class="btn btn-secondary" @click="closePasswordModal">Cancel</button>
            <button type="button" class="btn btn-primary" @click="handleChangePassword" :disabled="changingPassword">
              <span v-if="changingPassword" class="spinner-border spinner-border-sm me-2"></span>
              Change Password
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- Change Role Modal -->
    <div v-if="showRoleModal" class="modal fade show d-block" tabindex="-1" style="background-color: rgba(0,0,0,0.5)">
      <div class="modal-dialog">
        <div class="modal-content">
          <div class="modal-header">
            <h5 class="modal-title">Change Role: {{ selectedUser?.username }}</h5>
            <button type="button" class="btn-close" @click="closeRoleModal"></button>
          </div>
          <div class="modal-body">
            <div class="mb-3">
              <label for="newRole" class="form-label">Role</label>
              <select id="newRole" v-model="roleForm.role" class="form-select" @change="handleRoleModalChange">
                <option value="admin">Admin (Full Access)</option>
                <option value="org-admin">Org Admin (Manage Organization)</option>
                <option value="operator-admin">Operator Admin (Manage Accounts/Users)</option>
                <option value="account-admin">Account Admin (Manage Users)</option>
              </select>
            </div>
            <div v-if="roleForm.role === 'org-admin'" class="mb-3">
              <div class="alert alert-info mb-0 py-2 small">
                Organization assignment is set when the user is created and cannot be changed here.
              </div>
            </div>
            <div v-if="roleForm.role === 'operator-admin'" class="mb-3">
              <label for="modalOperator" class="form-label">Operator <span class="text-danger">*</span></label>
              <select id="modalOperator" v-model="roleForm.operatorId" class="form-select" required>
                <option value="">Select an operator...</option>
                <option v-for="operator in operators" :key="operator.id" :value="operator.id">
                  {{ operator.name }}
                </option>
              </select>
            </div>
            <div v-if="roleForm.role === 'account-admin'" class="mb-3">
              <label for="modalAccount" class="form-label">Account <span class="text-danger">*</span></label>
              <select id="modalAccount" v-model="roleForm.accountId" class="form-select" required>
                <option value="">Select an account...</option>
                <option v-for="account in accounts" :key="account.id" :value="account.id">
                  {{ account.name }} ({{ account.operatorName }})
                </option>
              </select>
            </div>
            <div v-if="roleError" class="alert alert-danger">{{ roleError }}</div>
          </div>
          <div class="modal-footer">
            <button type="button" class="btn btn-secondary" @click="closeRoleModal">Cancel</button>
            <button type="button" class="btn btn-primary" @click="handleChangeRole" :disabled="changingRole">
              <span v-if="changingRole" class="spinner-border spinner-border-sm me-2"></span>
              Change Role
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- Delete Confirmation Modal -->
    <div v-if="showDeleteModal" class="modal fade show d-block" tabindex="-1" style="background-color: rgba(0,0,0,0.5)">
      <div class="modal-dialog">
        <div class="modal-content">
          <div class="modal-header">
            <h5 class="modal-title">Delete Local User</h5>
            <button type="button" class="btn-close" @click="closeDeleteModal"></button>
          </div>
          <div class="modal-body">
            <p>Are you sure you want to delete user <strong>{{ selectedUser?.username }}</strong>?</p>
            <p class="text-danger">This action cannot be undone.</p>
            <div v-if="deleteError" class="alert alert-danger">{{ deleteError }}</div>
          </div>
          <div class="modal-footer">
            <button type="button" class="btn btn-secondary" @click="closeDeleteModal">Cancel</button>
            <button type="button" class="btn btn-danger" @click="handleDelete" :disabled="deleting">
              <span v-if="deleting" class="spinner-border spinner-border-sm me-2"></span>
              Delete
            </button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, watch, onMounted } from 'vue'
import apiClient from '@/utils/api'

// Operators with an empty organization_id belong to the default org.
const DEFAULT_ORG_ID = '00000000-0000-0000-0000-000000000001'

const users = ref([])
const loading = ref(false)
const error = ref('')

const operators = ref([])
const accounts = ref([])
const organizations = ref([])

const showCreateModal = ref(false)
const createForm = ref({ username: '', password: '', role: 'admin', operatorId: '', accountId: '', organizationId: '' })
const creating = ref(false)
const createError = ref('')

const showPasswordModal = ref(false)
const passwordForm = ref({ password: '' })
const changingPassword = ref(false)
const passwordError = ref('')

const showRoleModal = ref(false)
const roleForm = ref({ role: 'admin', operatorId: '', accountId: '', organizationId: '' })
const changingRole = ref(false)
const roleError = ref('')

const showDeleteModal = ref(false)
const selectedUser = ref(null)
const deleting = ref(false)
const deleteError = ref('')

const loadUsers = async () => {
  loading.value = true
  error.value = ''
  try {
    const response = await apiClient.post('/nis.v1.AuthService/ListAPIUsers', { authSource: 'local' })
    users.value = response.data.users || []
  } catch (err) {
    error.value = err.response?.data?.message || 'Failed to load users'
  } finally {
    loading.value = false
  }
}

const loadOrganizations = async () => {
  try {
    const response = await apiClient.post('/nis.v1.OrganizationService/ListOrganizations', { page: { limit: 200 } })
    organizations.value = response.data.organizations || []
  } catch (err) {
    console.error('Failed to load organizations:', err)
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

const loadAccounts = async () => {
  try {
    // Load all accounts from all operators
    const allAccounts = []
    for (const operator of operators.value) {
      const response = await apiClient.post('/nis.v1.AccountService/ListAccounts', {
        operatorId: operator.id
      })
      const operatorAccounts = (response.data.accounts || []).map(account => ({
        ...account,
        operatorName: operator.name,
        operatorOrgId: operator.organizationId || DEFAULT_ORG_ID
      }))
      allAccounts.push(...operatorAccounts)
    }
    accounts.value = allAccounts
  } catch (err) {
    console.error('Failed to load accounts:', err)
  }
}

// Operator/account pickers are scoped to the chosen organization so you can't
// pair an org with an operator from a different org. No org selected => empty.
const filteredOperators = computed(() => {
  const orgId = createForm.value.organizationId
  if (!orgId) return []
  return operators.value.filter((op) => (op.organizationId || DEFAULT_ORG_ID) === orgId)
})

const filteredAccounts = computed(() => {
  const orgId = createForm.value.organizationId
  if (!orgId) return []
  return accounts.value.filter((acc) => acc.operatorOrgId === orgId)
})

// Changing the org invalidates any operator/account picked under the old org.
watch(() => createForm.value.organizationId, () => {
  createForm.value.operatorId = ''
  createForm.value.accountId = ''
})

const handleRoleChange = () => {
  // Clear operator/account scope when role changes; org is role-independent.
  createForm.value.operatorId = ''
  createForm.value.accountId = ''
}

const handleRoleModalChange = () => {
  // Clear scope selections when role changes
  roleForm.value.operatorId = ''
  roleForm.value.accountId = ''
  roleForm.value.organizationId = ''
}

const closeCreateModal = () => {
  showCreateModal.value = false
  createForm.value = { username: '', password: '', role: 'admin', operatorId: '', accountId: '', organizationId: '' }
  createError.value = ''
}

const handleCreate = async () => {
  creating.value = true
  createError.value = ''
  try {
    const payload = {
      username: createForm.value.username,
      password: createForm.value.password,
      permissions: [createForm.value.role]
    }

    // Add scope id based on role
    if (createForm.value.role === 'org-admin') {
      if (!createForm.value.organizationId) {
        createError.value = 'Please select an organization'
        creating.value = false
        return
      }
    } else if (createForm.value.role === 'operator-admin') {
      if (!createForm.value.operatorId) {
        createError.value = 'Please select an operator'
        creating.value = false
        return
      }
      payload.operatorId = createForm.value.operatorId
    } else if (createForm.value.role === 'account-admin') {
      if (!createForm.value.accountId) {
        createError.value = 'Please select an account'
        creating.value = false
        return
      }
      payload.accountId = createForm.value.accountId
    }

    // Organization is role-independent: send it whenever one is chosen.
    if (createForm.value.organizationId) {
      payload.organizationId = createForm.value.organizationId
    }

    await apiClient.post('/nis.v1.AuthService/CreateAPIUser', payload)
    await loadUsers()
    closeCreateModal()
  } catch (err) {
    createError.value = err.response?.data?.message || 'Failed to create user'
  } finally {
    creating.value = false
  }
}

const showChangePasswordModal = (user) => {
  selectedUser.value = user
  passwordForm.value = { password: '' }
  showPasswordModal.value = true
}

const closePasswordModal = () => {
  showPasswordModal.value = false
  selectedUser.value = null
  passwordForm.value = { password: '' }
  passwordError.value = ''
}

const handleChangePassword = async () => {
  changingPassword.value = true
  passwordError.value = ''
  try {
    await apiClient.post('/nis.v1.AuthService/UpdateAPIUserPassword', {
      id: selectedUser.value.id,
      password: passwordForm.value.password
    })
    closePasswordModal()
  } catch (err) {
    passwordError.value = err.response?.data?.message || 'Failed to change password'
  } finally {
    changingPassword.value = false
  }
}

const showChangeRoleModal = (user) => {
  selectedUser.value = user
  roleForm.value = {
    role: user.permissions[0] || 'admin',
    operatorId: user.operatorId || '',
    accountId: user.accountId || '',
    organizationId: user.organizationId || ''
  }
  showRoleModal.value = true
}

const closeRoleModal = () => {
  showRoleModal.value = false
  selectedUser.value = null
  roleForm.value = { role: 'admin', operatorId: '', accountId: '', organizationId: '' }
  roleError.value = ''
}

const handleChangeRole = async () => {
  changingRole.value = true
  roleError.value = ''
  try {
    const payload = {
      id: selectedUser.value.id,
      permissions: [roleForm.value.role]
    }

    // Add operator_id or account_id based on role
    if (roleForm.value.role === 'operator-admin') {
      if (!roleForm.value.operatorId) {
        roleError.value = 'Please select an operator'
        changingRole.value = false
        return
      }
      payload.operatorId = roleForm.value.operatorId
    } else if (roleForm.value.role === 'account-admin') {
      if (!roleForm.value.accountId) {
        roleError.value = 'Please select an account'
        changingRole.value = false
        return
      }
      payload.accountId = roleForm.value.accountId
    }

    await apiClient.post('/nis.v1.AuthService/UpdateAPIUserPermissions', payload)
    await loadUsers()
    closeRoleModal()
  } catch (err) {
    roleError.value = err.response?.data?.message || 'Failed to change role'
  } finally {
    changingRole.value = false
  }
}

const confirmDelete = (user) => {
  selectedUser.value = user
  showDeleteModal.value = true
}

const closeDeleteModal = () => {
  showDeleteModal.value = false
  selectedUser.value = null
  deleteError.value = ''
}

const handleDelete = async () => {
  deleting.value = true
  deleteError.value = ''
  try {
    await apiClient.post('/nis.v1.AuthService/DeleteAPIUser', {
      id: selectedUser.value.id
    })
    await loadUsers()
    closeDeleteModal()
  } catch (err) {
    deleteError.value = err.response?.data?.message || 'Failed to delete user'
  } finally {
    deleting.value = false
  }
}

const getRoleBadgeClass = (role) => {
  switch (role) {
    case 'admin':
      return 'badge bg-danger'
    case 'org-admin':
      return 'badge bg-primary'
    case 'operator-admin':
      return 'badge bg-warning'
    case 'account-admin':
      return 'badge bg-info'
    default:
      return 'badge bg-secondary'
  }
}

const getRoleDisplay = (role) => {
  switch (role) {
    case 'admin':
      return 'Admin'
    case 'org-admin':
      return 'Org Admin'
    case 'operator-admin':
      return 'Operator Admin'
    case 'account-admin':
      return 'Account Admin'
    default:
      return role
  }
}

const orgName = (orgId) => {
  if (!orgId) return '—'
  const org = organizations.value.find((o) => o.id === orgId)
  return org ? org.name : orgId
}

const formatDate = (dateStr) => {
  if (!dateStr) return '-'
  return new Date(dateStr).toLocaleString()
}

onMounted(async () => {
  await loadOrganizations()
  await loadOperators()
  await loadAccounts()
  await loadUsers()
})
</script>

<style scoped>
.badge {
  font-size: 0.85rem;
  font-weight: 500;
}
</style>
