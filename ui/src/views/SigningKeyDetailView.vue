<template>
  <div class="container-fluid py-4">
    <div v-if="loading" class="text-center py-5">
      <div class="spinner-border text-primary" role="status"></div>
    </div>

    <div v-else-if="error" class="alert alert-danger">{{ error }}</div>

    <div v-else-if="signingKey">
      <div class="d-flex justify-content-between align-items-center mb-4">
        <h1>{{ signingKey.name }}</h1>
        <router-link to="/signing-keys" class="btn btn-outline-secondary">
          Back to Signing Keys
        </router-link>
      </div>

      <div class="row g-4">
        <div class="col-md-6">
          <div class="card">
            <div class="card-header">
              <h5 class="mb-0">Signing Key Details</h5>
            </div>
            <div class="card-body">
              <dl class="row mb-0">
                <dt class="col-sm-4">ID:</dt>
                <dd class="col-sm-8"><code>{{ signingKey.id }}</code></dd>

                <dt class="col-sm-4">Name:</dt>
                <dd class="col-sm-8">{{ signingKey.name }}</dd>

                <dt class="col-sm-4">Description:</dt>
                <dd class="col-sm-8">{{ signingKey.description || '-' }}</dd>

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
                <dd class="col-sm-8"><ClickablePubKey :pubkey="signingKey.publicKey" /></dd>

                <dt class="col-sm-4">Created:</dt>
                <dd class="col-sm-8">{{ formatDate(signingKey.createdAt) }}</dd>

                <dt class="col-sm-4">Updated:</dt>
                <dd class="col-sm-8">{{ formatDate(signingKey.updatedAt) }}</dd>
              </dl>
            </div>
          </div>
        </div>

        <div class="col-md-6">
          <div class="card">
            <div class="card-header">
              <h5 class="mb-0">Permissions</h5>
            </div>
            <div class="card-body">
              <h6 class="text-success">Publish Allow</h6>
              <PermList :subjects="signingKey.permissions?.pubAllow" empty-label="No publish allow rules" />

              <h6 class="text-danger mt-3">Publish Deny</h6>
              <PermList :subjects="signingKey.permissions?.pubDeny" empty-label="No publish deny rules" />

              <h6 class="text-success mt-3">Subscribe Allow</h6>
              <PermList :subjects="signingKey.permissions?.subAllow" empty-label="No subscribe allow rules" />

              <h6 class="text-danger mt-3">Subscribe Deny</h6>
              <PermList :subjects="signingKey.permissions?.subDeny" empty-label="No subscribe deny rules" />
            </div>
          </div>

          <div v-if="hasResponsePermission" class="card mt-3">
            <div class="card-header">
              <h5 class="mb-0">Response Permission</h5>
            </div>
            <div class="card-body">
              <dl class="row mb-0">
                <dt class="col-sm-4">Max Messages:</dt>
                <dd class="col-sm-8">{{ signingKey.responsePermission?.maxMsgs ?? 0 }}</dd>

                <dt class="col-sm-4">Expires (ns):</dt>
                <dd class="col-sm-8">{{ signingKey.responsePermission?.expires ?? 0 }}</dd>
              </dl>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, h } from 'vue'
import { useRoute } from 'vue-router'
import apiClient from '@/utils/api'
import ClickablePubKey from '@/components/ClickablePubKey.vue'

// Inline list helper — small enough to live here, not worth a separate component.
const PermList = (props) => {
  const list = props.subjects || []
  if (list.length === 0) {
    return h('div', { class: 'text-muted small' }, props.emptyLabel)
  }
  return h(
    'ul',
    { class: 'list-unstyled mb-0' },
    list.map((s) => h('li', null, [h('code', null, s)]))
  )
}
PermList.props = ['subjects', 'emptyLabel']

const route = useRoute()
const signingKey = ref(null)
const account = ref(null)
const operator = ref(null)
const loading = ref(false)
const error = ref('')

const hasResponsePermission = computed(() => {
  const rp = signingKey.value?.responsePermission
  if (!rp) return false
  return (rp.maxMsgs && rp.maxMsgs > 0) || (rp.expires && rp.expires > 0)
})

const loadKey = async () => {
  loading.value = true
  error.value = ''
  try {
    const response = await apiClient.post('/nis.v1.ScopedSigningKeyService/GetScopedSigningKey', {
      id: route.params.id
    })
    signingKey.value = response.data.key

    if (signingKey.value?.accountId) {
      try {
        const accResponse = await apiClient.post('/nis.v1.AccountService/GetAccount', {
          id: signingKey.value.accountId
        })
        account.value = accResponse.data.account

        if (account.value?.operatorId) {
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
    error.value = err.response?.data?.message || 'Failed to load signing key'
  } finally {
    loading.value = false
  }
}

const formatDate = (dateStr) => {
  if (!dateStr) return '-'
  return new Date(dateStr).toLocaleString()
}

onMounted(() => {
  loadKey()
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
