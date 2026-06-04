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

    <CodeBlock v-else-if="yaml" :content="yaml" label="config.yaml" />
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { configClient } from '@/utils/clients'
import CodeBlock from '@/components/CodeBlock.vue'

const yaml = ref('')
const loading = ref(false)
const error = ref('')

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

onMounted(loadConfig)
</script>
