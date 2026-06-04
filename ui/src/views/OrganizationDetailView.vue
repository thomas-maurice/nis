<template>
  <div class="container-fluid py-4">
    <div v-if="loading" class="text-center py-5">
      <div class="spinner-border text-primary" role="status"></div>
    </div>

    <div v-else-if="loadError" class="alert alert-danger">{{ loadError }}</div>

    <template v-else-if="org">
      <!-- Organization header card -->
      <div class="card mb-4">
        <div class="card-header d-flex justify-content-between align-items-center">
          <h5 class="mb-0">
            <font-awesome-icon :icon="['fas', 'building']" class="me-2" />
            {{ org.name }}
            <code class="ms-2 fs-6 text-secondary">{{ org.slug }}</code>
          </h5>
          <button v-if="authStore.isAdmin" class="btn btn-sm btn-outline-primary" @click="openEditOrgModal">Edit</button>
        </div>
        <div class="card-body">
          <p v-if="org.description" class="mb-1">{{ org.description }}</p>
          <div class="text-muted small">
            Created: {{ formatDate(org.createdAt) }} &nbsp;&bull;&nbsp;
            Updated: {{ formatDate(org.updatedAt) }}
          </div>
        </div>
      </div>

      <!-- SSO Configuration card (admin or org-admin for own org) -->
      <div v-if="authStore.isAdmin || isOwnOrg" class="card mb-4">
        <div class="card-header">
          <h5 class="mb-0">
            <font-awesome-icon :icon="['fas', 'shield-halved']" class="me-2" />
            SSO Configuration
          </h5>
        </div>
        <div class="card-body">
          <div v-if="ssoLoading" class="text-center py-3">
            <div class="spinner-border spinner-border-sm text-primary" role="status"></div>
          </div>
          <template v-else>
            <div v-if="ssoSuccess" class="alert alert-success alert-dismissible">
              {{ ssoSuccess }}
              <button type="button" class="btn-close" @click="ssoSuccess = ''"></button>
            </div>
            <div v-if="ssoError" class="alert alert-danger alert-dismissible">
              {{ ssoError }}
              <button type="button" class="btn-close" @click="ssoError = ''"></button>
            </div>

            <div class="mb-3">
              <label class="form-label">Redirect URI (callback)</label>
              <div v-if="ssoCallbackUrl" class="input-group">
                <input type="text" class="form-control font-monospace" :value="ssoCallbackUrl" readonly />
                <button type="button" class="btn btn-outline-secondary" @click="copyCallbackUrl" title="Copy">
                  <font-awesome-icon :icon="['fas', 'copy']" />
                </button>
              </div>
              <div v-else class="alert alert-warning mb-0 py-2">
                <code>server.public_url</code> is not configured on this server, so the redirect URI cannot be shown. Set it and restart NIS, then register the callback with your IdP.
              </div>
              <div v-if="ssoCallbackUrl" class="form-text">Register this exact URL as an allowed redirect URI / callback in your IdP.</div>
            </div>

            <form @submit.prevent="saveSSOConfig">
              <div class="mb-3 form-check">
                <input id="ssoEnabled" v-model="ssoForm.enabled" class="form-check-input" type="checkbox" />
                <label class="form-check-label" for="ssoEnabled">Enabled</label>
              </div>
              <div class="mb-3">
                <label for="issuerUrl" class="form-label">Issuer URL <span class="text-danger">*</span></label>
                <input id="issuerUrl" v-model="ssoForm.issuerUrl" type="url" class="form-control" placeholder="https://idp.example.com" />
              </div>
              <div class="mb-3">
                <label for="clientId" class="form-label">Client ID <span class="text-danger">*</span></label>
                <input id="clientId" v-model="ssoForm.clientId" type="text" class="form-control" />
              </div>
              <div class="mb-3">
                <label for="clientSecret" class="form-label">Client Secret</label>
                <input
                  id="clientSecret"
                  v-model="ssoForm.clientSecret"
                  type="password"
                  class="form-control"
                  :placeholder="ssoConfig && ssoConfig.clientSecretSet ? 'leave blank to keep existing' : 'enter client secret'"
                />
                <div v-if="ssoConfig && ssoConfig.clientSecretSet" class="form-text">
                  <span class="badge bg-success">secret stored</span>
                  A client secret is currently stored.
                </div>
              </div>
              <div class="mb-3">
                <label for="scopes" class="form-label">Scopes</label>
                <input id="scopes" v-model="ssoForm.scopes" type="text" class="form-control" placeholder="openid profile email groups" />
              </div>
              <div class="mb-3">
                <label for="groupClaim" class="form-label">Group Claim</label>
                <input id="groupClaim" v-model="ssoForm.groupClaim" type="text" class="form-control" placeholder="groups" />
              </div>
              <div class="mb-3">
                <label for="defaultRole" class="form-label">Default Role</label>
                <select id="defaultRole" v-model="ssoForm.defaultRole" class="form-select">
                  <option value="">Deny login when no mapping matches</option>
                  <option value="org-admin">org-admin</option>
                  <option value="operator-admin">operator-admin</option>
                  <option value="account-admin">account-admin</option>
                </select>
                <div class="form-text">Applied when no group mapping matches. "admin" is never assignable via SSO.</div>
              </div>

              <div class="d-flex gap-2">
                <button type="submit" class="btn btn-primary" :disabled="ssoSaving">
                  <span v-if="ssoSaving" class="spinner-border spinner-border-sm me-2"></span>
                  Save SSO Config
                </button>
                <button
                  v-if="ssoConfig"
                  type="button"
                  class="btn btn-outline-danger"
                  :disabled="ssoSaving"
                  @click="deleteSSOConfig"
                >
                  Disable SSO / Delete Config
                </button>
              </div>
            </form>
          </template>
        </div>
      </div>

      <!-- SSO Role Mappings card (admin or org-admin for own org) -->
      <div v-if="authStore.isAdmin || isOwnOrg" class="card mb-4">
        <div class="card-header">
          <h5 class="mb-0">
            <font-awesome-icon :icon="['fas', 'arrows-left-right-to-line']" class="me-2" />
            SSO Role Mappings
          </h5>
        </div>
        <div class="card-body">
          <div v-if="mappingsLoading" class="text-center py-3">
            <div class="spinner-border spinner-border-sm text-primary" role="status"></div>
          </div>
          <template v-else>
            <div v-if="mappingsSuccess" class="alert alert-success alert-dismissible">
              {{ mappingsSuccess }}
              <button type="button" class="btn-close" @click="mappingsSuccess = ''"></button>
            </div>
            <div v-if="mappingsError" class="alert alert-danger alert-dismissible">
              {{ mappingsError }}
              <button type="button" class="btn-close" @click="mappingsError = ''"></button>
            </div>

            <p class="text-muted small">
              Evaluated by ascending priority; first match wins. "admin" is never assignable via SSO.
            </p>

            <div class="table-responsive mb-3">
              <table class="table table-sm align-middle">
                <thead>
                  <tr>
                    <th>Group Value</th>
                    <th>Role</th>
                    <th>Scope</th>
                    <th>Priority</th>
                    <th></th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-if="mappingRows.length === 0">
                    <td colspan="5" class="text-center text-muted py-3">No mappings defined</td>
                  </tr>
                  <tr v-for="(row, idx) in mappingRows" :key="idx">
                    <td>
                      <input v-model="row.groupValue" type="text" class="form-control form-control-sm" placeholder="group-name" />
                    </td>
                    <td>
                      <select v-model="row.role" class="form-select form-select-sm" @change="onMappingRoleChange(row)">
                        <option value="org-admin">org-admin</option>
                        <option value="operator-admin">operator-admin</option>
                        <option value="account-admin">account-admin</option>
                      </select>
                    </td>
                    <td>
                      <div v-if="row.role === 'operator-admin'">
                        <select v-model="row.scopeOperatorId" class="form-select form-select-sm">
                          <option value="">Select operator...</option>
                          <option v-for="op in operators" :key="op.id" :value="op.id">{{ op.name }}</option>
                        </select>
                      </div>
                      <div v-else-if="row.role === 'account-admin'">
                        <input v-model="row.scopeAccountId" type="text" class="form-control form-control-sm" placeholder="Account UUID" />
                      </div>
                      <span v-else class="text-muted small">—</span>
                    </td>
                    <td>
                      <input v-model.number="row.priority" type="number" class="form-control form-control-sm" style="width: 80px" />
                    </td>
                    <td>
                      <button class="btn btn-sm btn-outline-danger" @click="removeMapping(idx)">
                        <font-awesome-icon :icon="['fas', 'trash']" />
                      </button>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>

            <div class="d-flex gap-2">
              <button class="btn btn-outline-secondary btn-sm" @click="addMapping">
                <font-awesome-icon :icon="['fas', 'plus']" class="me-1" />
                Add Mapping
              </button>
              <button class="btn btn-primary btn-sm" :disabled="mappingsSaving" @click="saveMappings">
                <span v-if="mappingsSaving" class="spinner-border spinner-border-sm me-2"></span>
                Save Mappings
              </button>
            </div>
          </template>
        </div>
      </div>

      <!-- Organization Users card (org-admin managing own org only) -->
      <div v-if="isOwnOrg" class="card mb-4">
        <div class="card-header d-flex justify-content-between align-items-center">
          <h5 class="mb-0">
            <font-awesome-icon :icon="['fas', 'users']" class="me-2" />
            Organization Users
          </h5>
          <button class="btn btn-sm btn-primary" @click="openCreateUserModal">
            <font-awesome-icon :icon="['fas', 'plus']" class="me-1" />
            Create User
          </button>
        </div>
        <div class="card-body">
          <div v-if="usersLoading" class="text-center py-3">
            <div class="spinner-border spinner-border-sm text-primary" role="status"></div>
          </div>
          <div v-else-if="usersError" class="alert alert-danger">{{ usersError }}</div>
          <div v-else class="table-responsive">
            <table class="table table-hover mb-0">
              <thead>
                <tr>
                  <th>Username</th>
                  <th>Role</th>
                  <th>Source</th>
                  <th>Created</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                <tr v-if="orgUsers.length === 0">
                  <td colspan="5" class="text-center text-muted py-3">No users found</td>
                </tr>
                <tr v-for="u in orgUsers" :key="u.id">
                  <td>{{ u.username }}</td>
                  <td><span class="badge bg-secondary">{{ u.permissions && u.permissions[0] || u.role || '-' }}</span></td>
                  <td><span :class="u.authSource === 'oidc' ? 'badge bg-info' : 'badge bg-light text-dark'">{{ u.authSource || 'local' }}</span></td>
                  <td>{{ formatDate(u.createdAt) }}</td>
                  <td>
                    <button
                      v-if="u.authSource !== 'oidc'"
                      class="btn btn-sm btn-outline-primary me-1"
                      title="Change Password"
                      @click="openPasswordModal(u)"
                    >
                      <font-awesome-icon :icon="['fas', 'key']" />
                    </button>
                    <button
                      class="btn btn-sm btn-outline-danger"
                      title="Delete"
                      @click="deleteOrgUser(u)"
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
    </template>

    <!-- Edit org modal -->
    <div v-if="showEditOrgModal" class="modal fade show d-block" tabindex="-1" style="background-color: rgba(0,0,0,0.5)">
      <div class="modal-dialog">
        <div class="modal-content">
          <div class="modal-header">
            <h5 class="modal-title">Edit Organization</h5>
            <button type="button" class="btn-close" @click="showEditOrgModal = false"></button>
          </div>
          <div class="modal-body">
            <div class="mb-3">
              <label class="form-label">Name</label>
              <input v-model="editOrgForm.name" type="text" class="form-control" required />
            </div>
            <div class="mb-3">
              <label class="form-label">Slug <span class="text-muted small">(immutable)</span></label>
              <input :value="org && org.slug" type="text" class="form-control" disabled />
            </div>
            <div class="mb-3">
              <label class="form-label">Description</label>
              <textarea v-model="editOrgForm.description" class="form-control" rows="2"></textarea>
            </div>
            <div v-if="editOrgError" class="alert alert-danger">{{ editOrgError }}</div>
          </div>
          <div class="modal-footer">
            <button class="btn btn-secondary" @click="showEditOrgModal = false">Cancel</button>
            <button class="btn btn-primary" :disabled="editOrgSaving" @click="saveOrgEdit">
              <span v-if="editOrgSaving" class="spinner-border spinner-border-sm me-2"></span>
              Save
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- Create user modal -->
    <div v-if="showCreateUserModal" class="modal fade show d-block" tabindex="-1" style="background-color: rgba(0,0,0,0.5)">
      <div class="modal-dialog">
        <div class="modal-content">
          <div class="modal-header">
            <h5 class="modal-title">Create Organization User</h5>
            <button type="button" class="btn-close" @click="showCreateUserModal = false"></button>
          </div>
          <div class="modal-body">
            <div class="mb-3">
              <label class="form-label">Username <span class="text-danger">*</span></label>
              <input v-model="createUserForm.username" type="text" class="form-control" required />
            </div>
            <div class="mb-3">
              <label class="form-label">Password <span class="text-danger">*</span></label>
              <input v-model="createUserForm.password" type="password" class="form-control" required />
            </div>
            <div class="mb-3">
              <label class="form-label">Role <span class="text-danger">*</span></label>
              <select v-model="createUserForm.role" class="form-select" @change="createUserForm.operatorId = ''; createUserForm.accountId = ''">
                <option value="operator-admin">operator-admin</option>
                <option value="account-admin">account-admin</option>
              </select>
            </div>
            <div v-if="createUserForm.role === 'operator-admin'" class="mb-3">
              <label class="form-label">Operator <span class="text-danger">*</span></label>
              <select v-model="createUserForm.operatorId" class="form-select" required>
                <option value="">Select an operator...</option>
                <option v-for="op in operators" :key="op.id" :value="op.id">{{ op.name }}</option>
              </select>
            </div>
            <div v-if="createUserForm.role === 'account-admin'" class="mb-3">
              <label class="form-label">Account ID <span class="text-danger">*</span></label>
              <input v-model="createUserForm.accountId" type="text" class="form-control" placeholder="Account UUID" required />
            </div>
            <div v-if="createUserError" class="alert alert-danger">{{ createUserError }}</div>
          </div>
          <div class="modal-footer">
            <button class="btn btn-secondary" @click="showCreateUserModal = false">Cancel</button>
            <button class="btn btn-primary" :disabled="creatingUser" @click="handleCreateUser">
              <span v-if="creatingUser" class="spinner-border spinner-border-sm me-2"></span>
              Create
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- Change password modal -->
    <div v-if="showPasswordModal" class="modal fade show d-block" tabindex="-1" style="background-color: rgba(0,0,0,0.5)">
      <div class="modal-dialog">
        <div class="modal-content">
          <div class="modal-header">
            <h5 class="modal-title">Change Password: {{ selectedUser && selectedUser.username }}</h5>
            <button type="button" class="btn-close" @click="showPasswordModal = false"></button>
          </div>
          <div class="modal-body">
            <div class="mb-3">
              <label class="form-label">New Password</label>
              <input v-model="passwordForm.password" type="password" class="form-control" required />
            </div>
            <div v-if="passwordError" class="alert alert-danger">{{ passwordError }}</div>
          </div>
          <div class="modal-footer">
            <button class="btn btn-secondary" @click="showPasswordModal = false">Cancel</button>
            <button class="btn btn-primary" :disabled="changingPassword" @click="handleChangePassword">
              <span v-if="changingPassword" class="spinner-border spinner-border-sm me-2"></span>
              Change Password
            </button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { organizationClient, operatorClient, authClient } from '@/utils/clients'
import apiClient from '@/utils/api'

const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()

const id = route.params.id
const isOwnOrg = computed(() => authStore.isOrgAdmin && authStore.organizationId === id)

// Org
const org = ref(null)
const loading = ref(true)
const loadError = ref('')

// SSO config
const ssoConfig = ref(null)
const ssoCallbackUrl = ref('')
const ssoLoading = ref(false)
const ssoSaving = ref(false)
const ssoError = ref('')
const ssoSuccess = ref('')
const ssoForm = ref({ enabled: false, issuerUrl: '', clientId: '', clientSecret: '', scopes: 'openid profile email groups', groupClaim: 'groups', defaultRole: '' })

// Role mappings
const mappingRows = ref([])
const mappingsLoading = ref(false)
const mappingsSaving = ref(false)
const mappingsError = ref('')
const mappingsSuccess = ref('')

// Operators (for mapping scopes)
const operators = ref([])

// Org users
const orgUsers = ref([])
const usersLoading = ref(false)
const usersError = ref('')

// Edit org modal
const showEditOrgModal = ref(false)
const editOrgForm = ref({ name: '', description: '' })
const editOrgSaving = ref(false)
const editOrgError = ref('')

// Create user modal
const showCreateUserModal = ref(false)
const createUserForm = ref({ username: '', password: '', role: 'operator-admin', operatorId: '', accountId: '' })
const creatingUser = ref(false)
const createUserError = ref('')

// Password modal
const showPasswordModal = ref(false)
const selectedUser = ref(null)
const passwordForm = ref({ password: '' })
const changingPassword = ref(false)
const passwordError = ref('')

const formatDate = (ts) => {
  if (!ts) return '-'
  const d = ts.toDate ? ts.toDate() : new Date(ts)
  return d.toLocaleString()
}

const loadOrg = async () => {
  try {
    const resp = await organizationClient.getOrganization({ id })
    org.value = resp.organization
  } catch (err) {
    loadError.value = err.message || 'Failed to load organization'
  } finally {
    loading.value = false
  }
}

const copyCallbackUrl = () => {
  if (!ssoCallbackUrl.value) return
  navigator.clipboard.writeText(ssoCallbackUrl.value).then(() => {
    ssoSuccess.value = 'Redirect URI copied to clipboard'
  }).catch(() => {
    ssoError.value = 'Could not copy to clipboard'
  })
}

const loadSSOConfig = async () => {
  ssoLoading.value = true
  try {
    const resp = await organizationClient.getSSOConfig({ organizationId: id })
    ssoConfig.value = resp.config || null
    ssoCallbackUrl.value = resp.callbackUrl || ''
    if (ssoConfig.value) {
      ssoForm.value = {
        enabled: ssoConfig.value.enabled,
        issuerUrl: ssoConfig.value.issuerUrl,
        clientId: ssoConfig.value.clientId,
        clientSecret: '',
        scopes: ssoConfig.value.scopes || 'openid profile email groups',
        groupClaim: ssoConfig.value.groupClaim || 'groups',
        defaultRole: ssoConfig.value.defaultRole || ''
      }
    }
  } catch (err) {
    // A not-found error means no config yet — treat as empty
    if (!err.message || !err.message.includes('not found')) {
      ssoError.value = err.message || 'Failed to load SSO config'
    }
  } finally {
    ssoLoading.value = false
  }
}

const saveSSOConfig = async () => {
  ssoSaving.value = true
  ssoError.value = ''
  ssoSuccess.value = ''
  try {
    const resp = await organizationClient.setSSOConfig({
      organizationId: id,
      enabled: ssoForm.value.enabled,
      issuerUrl: ssoForm.value.issuerUrl,
      clientId: ssoForm.value.clientId,
      clientSecret: ssoForm.value.clientSecret,
      scopes: ssoForm.value.scopes,
      groupClaim: ssoForm.value.groupClaim,
      defaultRole: ssoForm.value.defaultRole
    })
    ssoConfig.value = resp.config || null
    ssoForm.value.clientSecret = ''
    ssoSuccess.value = 'SSO configuration saved.'
  } catch (err) {
    ssoError.value = err.message || 'Failed to save SSO config'
  } finally {
    ssoSaving.value = false
  }
}

const deleteSSOConfig = async () => {
  if (!confirm('Delete SSO configuration? This will disable OIDC login for this organization.')) return
  ssoSaving.value = true
  ssoError.value = ''
  ssoSuccess.value = ''
  try {
    await organizationClient.deleteSSOConfig({ organizationId: id })
    ssoConfig.value = null
    ssoForm.value = { enabled: false, issuerUrl: '', clientId: '', clientSecret: '', scopes: 'openid profile email groups', groupClaim: 'groups', defaultRole: '' }
    ssoSuccess.value = 'SSO configuration deleted.'
  } catch (err) {
    ssoError.value = err.message || 'Failed to delete SSO config'
  } finally {
    ssoSaving.value = false
  }
}

const loadMappings = async () => {
  mappingsLoading.value = true
  try {
    const resp = await organizationClient.listSSORoleMappings({ organizationId: id })
    mappingRows.value = (resp.mappings || []).map(m => ({
      groupValue: m.groupValue,
      role: m.role,
      scopeOperatorId: m.scopeOperatorId || '',
      scopeAccountId: m.scopeAccountId || '',
      priority: m.priority || 0
    }))
  } catch (err) {
    mappingsError.value = err.message || 'Failed to load role mappings'
  } finally {
    mappingsLoading.value = false
  }
}

const addMapping = () => {
  mappingRows.value.push({ groupValue: '', role: 'org-admin', scopeOperatorId: '', scopeAccountId: '', priority: 0 })
}

const removeMapping = (idx) => {
  mappingRows.value.splice(idx, 1)
}

const onMappingRoleChange = (row) => {
  row.scopeOperatorId = ''
  row.scopeAccountId = ''
}

const saveMappings = async () => {
  mappingsSaving.value = true
  mappingsError.value = ''
  mappingsSuccess.value = ''
  try {
    await organizationClient.setSSORoleMappings({
      organizationId: id,
      mappings: mappingRows.value.map(r => ({
        groupValue: r.groupValue,
        role: r.role,
        scopeOperatorId: r.scopeOperatorId || '',
        scopeAccountId: r.scopeAccountId || '',
        priority: r.priority || 0
      }))
    })
    await loadMappings()
    mappingsSuccess.value = 'Role mappings saved.'
  } catch (err) {
    mappingsError.value = err.message || 'Failed to save role mappings'
  } finally {
    mappingsSaving.value = false
  }
}

const loadOperators = async () => {
  try {
    const resp = await operatorClient.listOperators({})
    operators.value = resp.operators || []
  } catch (err) {
    console.error('Failed to load operators', err)
  }
}

const loadOrgUsers = async () => {
  usersLoading.value = true
  usersError.value = ''
  try {
    const resp = await authClient.listAPIUsers({})
    orgUsers.value = resp.users || []
  } catch (err) {
    usersError.value = err.message || 'Failed to load users'
  } finally {
    usersLoading.value = false
  }
}

const openEditOrgModal = () => {
  editOrgForm.value = { name: org.value.name, description: org.value.description }
  editOrgError.value = ''
  showEditOrgModal.value = true
}

const saveOrgEdit = async () => {
  editOrgSaving.value = true
  editOrgError.value = ''
  try {
    const resp = await organizationClient.updateOrganization({
      id,
      name: editOrgForm.value.name,
      description: editOrgForm.value.description
    })
    org.value = resp.organization
    showEditOrgModal.value = false
  } catch (err) {
    editOrgError.value = err.message || 'Failed to update organization'
  } finally {
    editOrgSaving.value = false
  }
}

const openCreateUserModal = () => {
  createUserForm.value = { username: '', password: '', role: 'operator-admin', operatorId: '', accountId: '' }
  createUserError.value = ''
  showCreateUserModal.value = true
}

const handleCreateUser = async () => {
  creatingUser.value = true
  createUserError.value = ''
  try {
    const payload = {
      username: createUserForm.value.username,
      password: createUserForm.value.password,
      permissions: [createUserForm.value.role],
      organizationId: id
    }
    if (createUserForm.value.role === 'operator-admin') {
      if (!createUserForm.value.operatorId) {
        createUserError.value = 'Operator is required'
        creatingUser.value = false
        return
      }
      payload.operatorId = createUserForm.value.operatorId
    } else if (createUserForm.value.role === 'account-admin') {
      if (!createUserForm.value.accountId) {
        createUserError.value = 'Account ID is required'
        creatingUser.value = false
        return
      }
      payload.accountId = createUserForm.value.accountId
    }
    await apiClient.post('/nis.v1.AuthService/CreateAPIUser', payload)
    showCreateUserModal.value = false
    await loadOrgUsers()
  } catch (err) {
    createUserError.value = err.response?.data?.message || err.message || 'Failed to create user'
  } finally {
    creatingUser.value = false
  }
}

const openPasswordModal = (user) => {
  selectedUser.value = user
  passwordForm.value = { password: '' }
  passwordError.value = ''
  showPasswordModal.value = true
}

const handleChangePassword = async () => {
  changingPassword.value = true
  passwordError.value = ''
  try {
    await apiClient.post('/nis.v1.AuthService/UpdateAPIUserPassword', {
      id: selectedUser.value.id,
      password: passwordForm.value.password
    })
    showPasswordModal.value = false
  } catch (err) {
    passwordError.value = err.response?.data?.message || err.message || 'Failed to change password'
  } finally {
    changingPassword.value = false
  }
}

const deleteOrgUser = async (user) => {
  if (!confirm(`Delete user "${user.username}"?`)) return
  try {
    await apiClient.post('/nis.v1.AuthService/DeleteAPIUser', { id: user.id })
    await loadOrgUsers()
  } catch (err) {
    usersError.value = err.response?.data?.message || err.message || 'Failed to delete user'
  }
}

onMounted(async () => {
  // Access check
  if (!authStore.isAdmin && !isOwnOrg.value) {
    router.replace('/')
    return
  }

  await loadOrg()
  await loadOperators()

  if (authStore.isAdmin || isOwnOrg.value) {
    await loadSSOConfig()
    await loadMappings()
  }

  if (isOwnOrg.value) {
    await loadOrgUsers()
  }
})
</script>
