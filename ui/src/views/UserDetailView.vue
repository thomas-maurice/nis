<template>
  <div class="container-fluid py-4">
    <div v-if="loading" class="text-center py-5">
      <div class="spinner-border text-primary" role="status"></div>
    </div>

    <div v-else-if="error" class="alert alert-danger">{{ error }}</div>

    <div v-else-if="user">
      <div class="d-flex justify-content-between align-items-center mb-4">
        <h1>{{ user.name }}</h1>
        <div>
          <button class="btn btn-primary me-2" @click="downloadCreds">
            <font-awesome-icon :icon="['fas', 'download']" class="me-2" />
            Download Credentials
          </button>
          <router-link to="/users" class="btn btn-outline-secondary">
            Back to Users
          </router-link>
        </div>
      </div>

      <div class="row g-4">
        <div class="col-md-6">
          <div class="card">
            <div class="card-header">
              <h5 class="mb-0">User Details</h5>
            </div>
            <div class="card-body">
              <dl class="row mb-0">
                <dt class="col-sm-4">ID:</dt>
                <dd class="col-sm-8"><code>{{ user.id }}</code></dd>

                <dt class="col-sm-4">Name:</dt>
                <dd class="col-sm-8">{{ user.name }}</dd>

                <dt class="col-sm-4">Description:</dt>
                <dd class="col-sm-8">{{ user.description || '-' }}</dd>

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
                <dd class="col-sm-8"><ClickablePubKey :pubkey="user.publicKey" /></dd>

                <dt class="col-sm-4">Created:</dt>
                <dd class="col-sm-8">{{ formatDate(user.createdAt) }}</dd>
              </dl>
            </div>
          </div>
        </div>

        <div class="col-md-6">
          <div class="card">
            <div class="card-header">
              <h5 class="mb-0">User JWT</h5>
            </div>
            <div class="card-body">
              <CodeBlock :content="user.jwt" label="" />
            </div>
          </div>
        </div>
      </div>

      <div v-if="credentials" class="row mt-4">
        <div class="col-12">
          <div class="card">
            <div class="card-header">
              <h5 class="mb-0">User Credentials (.creds file)</h5>
            </div>
            <div class="card-body">
              <CodeBlock :content="credentials" label="" />
              <div class="alert alert-warning mt-3 mb-0">
                <strong>Security Notice:</strong> Keep these credentials secure. They provide full access as this user.
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import apiClient from '@/utils/api'
import CodeBlock from '@/components/CodeBlock.vue'
import ClickablePubKey from '@/components/ClickablePubKey.vue'

const route = useRoute()
const user = ref(null)
const account = ref(null)
const operator = ref(null)
const credentials = ref('')
const loading = ref(false)
const error = ref('')

const loadUser = async () => {
  loading.value = true
  error.value = ''
  try {
    const response = await apiClient.post('/nis.v1.UserService/GetUser', {
      id: route.params.id
    })
    user.value = response.data.user

    // Load account details
    if (user.value.accountId) {
      try {
        const accResponse = await apiClient.post('/nis.v1.AccountService/GetAccount', {
          id: user.value.accountId
        })
        account.value = accResponse.data.account

        // Load operator details
        if (account.value.operatorId) {
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
    error.value = err.response?.data?.message || 'Failed to load user'
  } finally {
    loading.value = false
  }
}

const downloadCreds = async () => {
  try {
    const response = await apiClient.post('/nis.v1.UserService/GetUserCredentials', {
      id: user.value.id
    })
    credentials.value = response.data.credentials

    // Trigger download
    const blob = new Blob([credentials.value], { type: 'text/plain' })
    const url = window.URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = `${user.value.name}.creds`
    link.click()
    window.URL.revokeObjectURL(url)
  } catch (err) {
    error.value = err.response?.data?.message || 'Failed to get credentials'
  }
}

const formatDate = (dateStr) => {
  if (!dateStr) return '-'
  return new Date(dateStr).toLocaleString()
}

onMounted(() => {
  loadUser()
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
