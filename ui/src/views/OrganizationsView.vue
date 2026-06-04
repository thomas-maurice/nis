<template>
  <div class="container-fluid py-4">
    <EntityList
      title="Organizations"
      entity-name="Organization"
      :items="organizations"
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
      <template #cell-createdAt="{ item }">
        {{ formatDate(item.createdAt) }}
      </template>
    </EntityList>

    <div class="d-flex align-items-center gap-2 mt-3">
      <button class="btn btn-outline-secondary btn-sm" :disabled="cursorStack.length === 0 || loading" @click="loadFirstPage">First</button>
      <button class="btn btn-outline-secondary btn-sm" :disabled="cursorStack.length === 0 || loading" @click="prevPage">Prev</button>
      <span class="text-muted small">Page {{ cursorStack.length + 1 }}</span>
      <button class="btn btn-outline-secondary btn-sm" :disabled="!nextCursor || loading" @click="nextPage">Next</button>
    </div>

    <EntityForm
      v-if="showModal"
      :title="editingOrg ? 'Edit Organization' : 'Create Organization'"
      :submit-label="editingOrg ? 'Update' : 'Create'"
      :initial-data="formData"
      :loading="saving"
      :error="formError"
      @submit="handleSubmit"
      @close="closeModal"
    >
      <template #fields="{ formData }">
        <div class="mb-3">
          <label for="orgName" class="form-label">Name <span class="text-danger">*</span></label>
          <input
            id="orgName"
            v-model="formData.name"
            type="text"
            class="form-control"
            placeholder="My Organization"
            required
          />
        </div>

        <div class="mb-3">
          <label for="orgSlug" class="form-label">Slug <span class="text-danger">*</span></label>
          <input
            id="orgSlug"
            v-model="formData.slug"
            type="text"
            class="form-control"
            placeholder="my-org"
            :disabled="!!editingOrg"
            required
          />
          <div class="form-text">Lowercase letters, digits, hyphens. Immutable after creation.</div>
        </div>

        <div class="mb-3">
          <label for="orgDescription" class="form-label">Description</label>
          <textarea
            id="orgDescription"
            v-model="formData.description"
            class="form-control"
            rows="2"
          ></textarea>
        </div>
      </template>
    </EntityForm>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { organizationClient } from '@/utils/clients'
import EntityList from '@/components/EntityList.vue'
import EntityForm from '@/components/EntityForm.vue'

const router = useRouter()
const authStore = useAuthStore()
const organizations = ref([])
const loading = ref(false)
const error = ref('')
const nextCursor = ref('')
const cursorStack = ref([])
const showModal = ref(false)
const editingOrg = ref(null)
const formData = ref({})
const saving = ref(false)
const formError = ref('')

const columns = [
  { key: 'name', label: 'Name' },
  { key: 'slug', label: 'Slug' },
  { key: 'description', label: 'Description' },
  { key: 'createdAt', label: 'Created' }
]

const formatDate = (ts) => {
  if (!ts) return '-'
  const d = ts.toDate ? ts.toDate() : new Date(ts)
  return d.toLocaleString()
}

const loadPage = async (cursor) => {
  loading.value = true
  error.value = ''
  try {
    const resp = await organizationClient.listOrganizations({ page: { limit: 50, cursor: cursor || '' } })
    organizations.value = resp.organizations || []
    nextCursor.value = resp.nextCursor || ''
  } catch (err) {
    error.value = err.message || 'Failed to load organizations'
  } finally {
    loading.value = false
  }
}

const loadFirstPage = () => {
  cursorStack.value = []
  loadPage('')
}

const nextPage = () => {
  if (!nextCursor.value) return
  cursorStack.value.push(nextCursor.value)
  loadPage(nextCursor.value)
}

const prevPage = () => {
  if (cursorStack.value.length === 0) return
  cursorStack.value.pop()
  const prev = cursorStack.value.length > 0 ? cursorStack.value[cursorStack.value.length - 1] : ''
  loadPage(prev)
}

const showCreateModal = () => {
  editingOrg.value = null
  formData.value = { name: '', slug: '', description: '' }
  showModal.value = true
  formError.value = ''
}

const showEditModal = (org) => {
  editingOrg.value = org
  formData.value = { name: org.name, slug: org.slug, description: org.description }
  showModal.value = true
  formError.value = ''
}

const closeModal = () => {
  showModal.value = false
  editingOrg.value = null
  formData.value = {}
  formError.value = ''
}

const handleSubmit = async (data) => {
  saving.value = true
  formError.value = ''
  try {
    if (editingOrg.value) {
      await organizationClient.updateOrganization({
        id: editingOrg.value.id,
        name: data.name,
        description: data.description
      })
    } else {
      await organizationClient.createOrganization({
        name: data.name,
        slug: data.slug,
        description: data.description
      })
    }
    closeModal()
    loadFirstPage()
  } catch (err) {
    formError.value = err.message || 'Failed to save organization'
  } finally {
    saving.value = false
  }
}

const handleDelete = async (org) => {
  if (!confirm(`Delete organization "${org.name}"? This cannot be undone.`)) return
  try {
    await organizationClient.deleteOrganization({ id: org.id })
    loadFirstPage()
  } catch (err) {
    error.value = err.message || 'Failed to delete organization'
  }
}

const handleSelect = (org) => {
  router.push('/organizations/' + org.id)
}

onMounted(() => {
  loadFirstPage()
})
</script>
