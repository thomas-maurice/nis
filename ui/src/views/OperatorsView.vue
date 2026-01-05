<template>
  <div class="container-fluid py-4">
    <EntityList
      title="Operators"
      entity-name="Operator"
      :items="operators"
      :columns="columns"
      :loading="loading"
      :error="error"
      :can-create="authStore.isAdmin"
      :can-edit="authStore.isAdmin"
      :can-delete="authStore.isAdmin"
      @create="showCreateModal"
      @edit="showEditModal"
      @delete="handleDelete"
      @select="handleSelect"
    >
      <template v-if="authStore.isAdmin" #header-actions>
        <button class="btn btn-outline-primary me-2" @click="showImportModal = true">
          <font-awesome-icon :icon="['fas', 'file-import']" class="me-2" />
          Import
        </button>
        <button class="btn btn-outline-info me-2" @click="showNSCImportModal = true">
          <font-awesome-icon :icon="['fas', 'file-import']" class="me-2" />
          Import from NSC
        </button>
      </template>
      <template #cell-publicKey="{ item }">
        <ClickablePubKey :pubkey="item.publicKey" />
      </template>

      <template #cell-systemAccountPubKey="{ item }">
        <span v-if="item.systemAccountPubKey">
          <ClickablePubKey :pubkey="item.systemAccountPubKey" />
        </span>
        <span v-else class="text-muted">Not set</span>
      </template>

      <template #cell-createdAt="{ item }">
        {{ formatDate(item.createdAt) }}
      </template>
    </EntityList>

    <EntityForm
      v-if="showModal"
      :title="editingOperator ? 'Edit Operator' : 'Create Operator'"
      :submit-label="editingOperator ? 'Update' : 'Create'"
      :initial-data="formData"
      :loading="saving"
      :error="formError"
      @submit="handleSubmit"
      @close="closeModal"
    >
      <template #fields="{ formData }">
        <div class="mb-3">
          <label for="name" class="form-label">Name <span class="text-danger">*</span></label>
          <input
            id="name"
            v-model="formData.name"
            type="text"
            class="form-control"
            placeholder="my-operator"
            required
            :disabled="editingOperator"
          />
          <div class="form-text">Unique name for the operator</div>
        </div>

        <div class="mb-3">
          <label for="description" class="form-label">Description</label>
          <textarea
            id="description"
            v-model="formData.description"
            class="form-control"
            rows="3"
            placeholder="Optional description"
          ></textarea>
        </div>

        <div class="mb-3">
          <label for="systemAccountPubKey" class="form-label">System Account Public Key</label>
          <input
            id="systemAccountPubKey"
            v-model="formData.systemAccountPubKey"
            type="text"
            class="form-control"
            placeholder="AXXXXXXXXXXXXX..."
          />
          <div class="form-text">Public key of the system account (usually $SYS)</div>
        </div>
      </template>
    </EntityForm>

    <!-- Import Operator Modal -->
    <div v-if="showImportModal" class="modal fade show d-block" tabindex="-1" style="background-color: rgba(0,0,0,0.5)">
      <div class="modal-dialog">
        <div class="modal-content">
          <div class="modal-header">
            <h5 class="modal-title">Import Operator</h5>
            <button type="button" class="btn-close" @click="closeImportModal"></button>
          </div>
          <div class="modal-body">
            <div class="mb-3">
              <label for="importFile" class="form-label">Select Export File</label>
              <input
                id="importFile"
                type="file"
                class="form-control"
                accept=".json"
                @change="handleFileSelect"
              />
              <div class="form-text">
                Select a JSON file exported from another NIS instance.
              </div>
            </div>
            <div class="mb-3">
              <div class="form-check">
                <input
                  id="regenerateIds"
                  v-model="importRegenerateIds"
                  class="form-check-input"
                  type="checkbox"
                />
                <label class="form-check-label" for="regenerateIds">
                  Regenerate IDs
                </label>
                <div class="form-text">
                  Generate new UUIDs for all entities. Use this to create a copy instead of restoring.
                </div>
              </div>
            </div>
            <div v-if="importError" class="alert alert-danger">{{ importError }}</div>
          </div>
          <div class="modal-footer">
            <button type="button" class="btn btn-secondary" @click="closeImportModal">Cancel</button>
            <button type="button" class="btn btn-primary" @click="handleImport" :disabled="!selectedFile || importing">
              <span v-if="importing" class="spinner-border spinner-border-sm me-2"></span>
              Import
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- Import from NSC Modal -->
    <div v-if="showNSCImportModal" class="modal fade show d-block" tabindex="-1" style="background-color: rgba(0,0,0,0.5)">
      <div class="modal-dialog">
        <div class="modal-content">
          <div class="modal-header">
            <h5 class="modal-title">Import from NSC</h5>
            <button type="button" class="btn-close" @click="closeNSCImportModal"></button>
          </div>
          <div class="modal-body">
            <div class="mb-3">
              <label for="nscArchive" class="form-label">Select NSC Archive</label>
              <input
                id="nscArchive"
                type="file"
                class="form-control"
                accept=".zip,.tar.gz,.tgz,.tar.bz2,.tbz2"
                @change="handleNSCFileSelect"
              />
              <div class="form-text">
                Select a compressed archive (.zip, .tar.gz, .tar.bz2) of your NSC store directory.
              </div>
            </div>
            <div class="mb-3">
              <label for="nscOperatorName" class="form-label">Operator Name</label>
              <input
                id="nscOperatorName"
                v-model="nscOperatorName"
                type="text"
                class="form-control"
                placeholder="my-operator"
              />
              <div class="form-text">
                Name of the operator to import from the NSC archive.
              </div>
            </div>
            <div v-if="nscImportError" class="alert alert-danger">{{ nscImportError }}</div>
          </div>
          <div class="modal-footer">
            <button type="button" class="btn btn-secondary" @click="closeNSCImportModal">Cancel</button>
            <button type="button" class="btn btn-primary" @click="handleNSCImport" :disabled="!nscArchiveFile || !nscOperatorName || nscImporting">
              <span v-if="nscImporting" class="spinner-border spinner-border-sm me-2"></span>
              Import
            </button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import apiClient from '@/utils/api'
import EntityList from '@/components/EntityList.vue'
import EntityForm from '@/components/EntityForm.vue'
import ClickablePubKey from '@/components/ClickablePubKey.vue'

const router = useRouter()
const authStore = useAuthStore()
const operators = ref([])
const loading = ref(false)
const error = ref('')
const showModal = ref(false)
const editingOperator = ref(null)
const formData = ref({})
const saving = ref(false)
const formError = ref('')
const showImportModal = ref(false)
const selectedFile = ref(null)
const importRegenerateIds = ref(false)
const importing = ref(false)
const importError = ref('')
const showNSCImportModal = ref(false)
const nscArchiveFile = ref(null)
const nscOperatorName = ref('')
const nscImporting = ref(false)
const nscImportError = ref('')

const columns = [
  { key: 'name', label: 'Name' },
  { key: 'description', label: 'Description' },
  { key: 'publicKey', label: 'Public Key' },
  { key: 'systemAccountPubKey', label: 'System Account' },
  { key: 'createdAt', label: 'Created' }
]

const loadOperators = async () => {
  loading.value = true
  error.value = ''
  try {
    const response = await apiClient.post('/nis.v1.OperatorService/ListOperators', {})
    operators.value = response.data.operators || []
  } catch (err) {
    error.value = err.response?.data?.message || 'Failed to load operators'
  } finally {
    loading.value = false
  }
}

const showCreateModal = () => {
  editingOperator.value = null
  formData.value = { name: '', description: '', systemAccountPubKey: '' }
  showModal.value = true
  formError.value = ''
}

const showEditModal = (operator) => {
  editingOperator.value = operator
  formData.value = { ...operator }
  showModal.value = true
  formError.value = ''
}

const closeModal = () => {
  showModal.value = false
  editingOperator.value = null
  formData.value = {}
  formError.value = ''
}

const handleSubmit = async (data) => {
  saving.value = true
  formError.value = ''
  try {
    if (editingOperator.value) {
      await apiClient.post('/nis.v1.OperatorService/UpdateOperator', {
        id: editingOperator.value.id,
        ...data
      })
    } else {
      await apiClient.post('/nis.v1.OperatorService/CreateOperator', data)
    }
    closeModal()
    await loadOperators()
  } catch (err) {
    formError.value = err.response?.data?.message || 'Failed to save operator'
  } finally {
    saving.value = false
  }
}

const handleDelete = async (operator) => {
  if (!confirm(`Are you sure you want to delete operator "${operator.name}"?`)) {
    return
  }

  try {
    await apiClient.post('/nis.v1.OperatorService/DeleteOperator', {
      id: operator.id
    })
    await loadOperators()
  } catch (err) {
    error.value = err.response?.data?.message || 'Failed to delete operator'
  }
}

const handleSelect = (operator) => {
  router.push(`/operators/${operator.id}`)
}

const truncate = (str, length) => {
  if (!str) return ''
  return str.length > length ? str.substring(0, length) + '...' : str
}

const closeImportModal = () => {
  showImportModal.value = false
  selectedFile.value = null
  importRegenerateIds.value = false
  importError.value = ''
}

const handleFileSelect = (event) => {
  const file = event.target.files[0]
  if (file) {
    selectedFile.value = file
  }
}

const handleImport = async () => {
  if (!selectedFile.value) return

  importing.value = true
  importError.value = ''
  try {
    // Read the file
    const reader = new FileReader()
    reader.onload = async (e) => {
      try {
        const fileContent = e.target.result
        // Convert to base64
        const base64Data = btoa(fileContent)

        await apiClient.post('/nis.v1.ExportService/ImportOperator', {
          data: base64Data,
          regenerateIds: importRegenerateIds.value
        })

        closeImportModal()
        await loadOperators()
      } catch (err) {
        importError.value = err.response?.data?.message || 'Failed to import operator'
      } finally {
        importing.value = false
      }
    }
    reader.onerror = () => {
      importError.value = 'Failed to read file'
      importing.value = false
    }
    reader.readAsText(selectedFile.value)
  } catch (err) {
    importError.value = err.message || 'Failed to import operator'
    importing.value = false
  }
}

const closeNSCImportModal = () => {
  showNSCImportModal.value = false
  nscArchiveFile.value = null
  nscOperatorName.value = ''
  nscImportError.value = ''
}

const handleNSCFileSelect = (event) => {
  const file = event.target.files[0]
  if (file) {
    nscArchiveFile.value = file
  }
}

const handleNSCImport = async () => {
  if (!nscArchiveFile.value) return

  nscImporting.value = true
  nscImportError.value = ''
  try {
    // Read the file
    const reader = new FileReader()
    reader.onload = async (e) => {
      try {
        const fileContent = e.target.result
        // Convert to base64
        const base64Data = btoa(
          new Uint8Array(fileContent)
            .reduce((data, byte) => data + String.fromCharCode(byte), '')
        )

        await apiClient.post('/nis.v1.ExportService/ImportFromNSC', {
          data: base64Data,
          operatorName: nscOperatorName.value
        })

        closeNSCImportModal()
        await loadOperators()
      } catch (err) {
        nscImportError.value = err.response?.data?.message || 'Failed to import from NSC'
      } finally {
        nscImporting.value = false
      }
    }
    reader.onerror = () => {
      nscImportError.value = 'Failed to read file'
      nscImporting.value = false
    }
    reader.readAsArrayBuffer(nscArchiveFile.value)
  } catch (err) {
    nscImportError.value = err.message || 'Failed to import from NSC'
    nscImporting.value = false
  }
}

const formatDate = (dateStr) => {
  if (!dateStr) return '-'
  return new Date(dateStr).toLocaleString()
}

onMounted(() => {
  loadOperators()
})
</script>
