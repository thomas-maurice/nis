<template>
  <div class="container-fluid py-4">
    <div class="d-flex justify-content-between align-items-center mb-4">
      <h1>Runtime Configuration</h1>
      <button class="btn btn-outline-primary" @click="loadConfig" :disabled="loading">
        <span v-if="loading" class="spinner-border spinner-border-sm me-2"></span>
        <font-awesome-icon v-else :icon="['fas', 'sync']" class="me-1" />
        Refresh
      </button>
    </div>

    <div class="alert alert-info">
      <font-awesome-icon :icon="['fas', 'circle-info']" class="me-2" />
      Effective configuration after the
      <code>flag &gt; env &gt; config file &gt; default</code> precedence chain.
      Credential values are redacted as <code>***REDACTED***</code> — key names are preserved
      so you can confirm a secret IS configured without leaking its value.
    </div>

    <div v-if="error" class="alert alert-danger">{{ error }}</div>

    <div v-if="loading && !yaml" class="text-center py-5">
      <div class="spinner-border text-primary" role="status"></div>
    </div>

    <div v-else-if="yaml" class="card">
      <div class="card-body">
        <div class="d-flex justify-content-end mb-2">
          <button
            class="btn btn-sm"
            :class="copied ? 'btn-success' : 'btn-outline-secondary'"
            @click="copyToClipboard"
          >
            <font-awesome-icon :icon="['fas', 'copy']" class="me-1" />
            {{ copied ? 'Copied!' : 'Copy YAML' }}
          </button>
        </div>
        <pre class="config-yaml mb-0"><code>{{ yaml }}</code></pre>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { configClient } from '@/utils/clients'

const yaml = ref('')
const loading = ref(false)
const error = ref('')
const copied = ref(false)

const loadConfig = async () => {
  loading.value = true
  error.value = ''
  try {
    const resp = await configClient.getRunningConfig({})
    yaml.value = resp.yaml || ''
  } catch (err) {
    error.value = err.message || 'Failed to load running configuration'
  } finally {
    loading.value = false
  }
}

const copyToClipboard = async () => {
  try {
    await navigator.clipboard.writeText(yaml.value)
    copied.value = true
    setTimeout(() => {
      copied.value = false
    }, 2000)
  } catch (err) {
    console.error('Failed to copy:', err)
  }
}

onMounted(loadConfig)
</script>

<style scoped>
.config-yaml {
  background-color: #f8f9fa;
  border: 1px solid #dee2e6;
  border-radius: 0.25rem;
  padding: 1rem;
  font-size: 0.875rem;
  max-height: 70vh;
  overflow: auto;
}
.config-yaml code {
  background-color: transparent;
  padding: 0;
  color: #212529;
  white-space: pre;
}
</style>
