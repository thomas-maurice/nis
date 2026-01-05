<template>
  <div class="container-fluid py-4">
    <div v-if="loading" class="text-center py-5">
      <div class="spinner-border text-primary" role="status"></div>
    </div>

    <div v-else-if="error" class="alert alert-danger">{{ error }}</div>

    <div v-else-if="account">
      <div class="d-flex justify-content-between align-items-center mb-4">
        <h1>{{ account.name }}</h1>
        <router-link to="/accounts" class="btn btn-outline-secondary">
          Back to Accounts
        </router-link>
      </div>

      <div class="row g-4">
        <div class="col-md-6">
          <div class="card">
            <div class="card-header">
              <h5 class="mb-0">Account Details</h5>
            </div>
            <div class="card-body">
              <dl class="row mb-0">
                <dt class="col-sm-4">ID:</dt>
                <dd class="col-sm-8"><code>{{ account.id }}</code></dd>

                <dt class="col-sm-4">Name:</dt>
                <dd class="col-sm-8">{{ account.name }}</dd>

                <dt class="col-sm-4">Description:</dt>
                <dd class="col-sm-8">{{ account.description || '-' }}</dd>

                <dt class="col-sm-4">Operator:</dt>
                <dd class="col-sm-8">
                  <router-link v-if="operator" :to="`/operators/${operator.id}`">{{ operator.name }}</router-link>
                  <span v-else class="text-muted">-</span>
                </dd>

                <dt class="col-sm-4">Public Key:</dt>
                <dd class="col-sm-8"><ClickablePubKey :pubkey="account.publicKey" /></dd>

                <dt class="col-sm-4">JetStream:</dt>
                <dd class="col-sm-8">
                  <span :class="account.jetstreamLimits?.enabled ? 'badge bg-success' : 'badge bg-secondary'">
                    {{ account.jetstreamLimits?.enabled ? 'Enabled' : 'Disabled' }}
                  </span>
                </dd>

                <dt class="col-sm-4">Created:</dt>
                <dd class="col-sm-8">{{ formatDate(account.createdAt) }}</dd>
              </dl>
            </div>
          </div>

          <div v-if="account.jetstreamLimits?.enabled" class="card mt-3">
            <div class="card-header">
              <h5 class="mb-0">JetStream Limits</h5>
            </div>
            <div class="card-body">
              <dl class="row mb-0">
                <dt class="col-sm-6">Max Memory:</dt>
                <dd class="col-sm-6">{{ formatLimit(account.jetstreamLimits?.maxMemory) }}</dd>

                <dt class="col-sm-6">Max Storage:</dt>
                <dd class="col-sm-6">{{ formatLimit(account.jetstreamLimits?.maxStorage) }}</dd>

                <dt class="col-sm-6">Max Streams:</dt>
                <dd class="col-sm-6">{{ formatLimit(account.jetstreamLimits?.maxStreams) }}</dd>

                <dt class="col-sm-6">Max Consumers:</dt>
                <dd class="col-sm-6">{{ formatLimit(account.jetstreamLimits?.maxConsumers) }}</dd>
              </dl>
            </div>
          </div>
        </div>

        <div class="col-md-6">
          <div class="card">
            <div class="card-header">
              <h5 class="mb-0">Account JWT</h5>
            </div>
            <div class="card-body">
              <CodeBlock :content="account.jwt" label="" />
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
const account = ref(null)
const operator = ref(null)
const loading = ref(false)
const error = ref('')

const loadAccount = async () => {
  loading.value = true
  error.value = ''
  try {
    const response = await apiClient.post('/nis.v1.AccountService/GetAccount', {
      id: route.params.id
    })
    account.value = response.data.account

    // Load operator details
    if (account.value.operatorId) {
      try {
        const opResponse = await apiClient.post('/nis.v1.OperatorService/GetOperator', {
          id: account.value.operatorId
        })
        operator.value = opResponse.data.operator
      } catch (err) {
        console.error('Failed to load operator:', err)
      }
    }
  } catch (err) {
    error.value = err.response?.data?.message || 'Failed to load account'
  } finally {
    loading.value = false
  }
}

const formatDate = (dateStr) => {
  if (!dateStr) return '-'
  return new Date(dateStr).toLocaleString()
}

const formatLimit = (value) => {
  if (value === -1 || value === '-1') return 'Unlimited'
  if (value === 0) return 'None'
  return value.toLocaleString()
}

onMounted(() => {
  loadAccount()
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
