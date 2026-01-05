<template>
  <div class="code-block">
    <div class="d-flex justify-content-between align-items-center mb-2">
      <label v-if="label" class="form-label mb-0">{{ label }}</label>
      <button
        v-if="canCopy"
        type="button"
        class="btn btn-sm"
        :class="copied ? 'btn-success' : 'btn-outline-secondary'"
        @click="copyToClipboard"
        :title="copied ? 'Copied!' : 'Copy to clipboard'"
      >
        <font-awesome-icon :icon="['fas', 'copy']" class="me-1" />
        {{ copied ? 'Copied!' : 'Copy' }}
      </button>
    </div>
    <pre class="mb-0"><code>{{ content }}</code></pre>
  </div>
</template>

<script setup>
import { ref } from 'vue'

const props = defineProps({
  content: {
    type: String,
    required: true
  },
  label: {
    type: String,
    default: ''
  },
  canCopy: {
    type: Boolean,
    default: true
  }
})

const copied = ref(false)

const copyToClipboard = async () => {
  try {
    await navigator.clipboard.writeText(props.content)
    copied.value = true
    setTimeout(() => {
      copied.value = false
    }, 2000)
  } catch (err) {
    console.error('Failed to copy:', err)
  }
}
</script>

<style scoped>
.code-block pre {
  background-color: #f8f9fa;
  border: 1px solid #dee2e6;
  border-radius: 0.25rem;
  padding: 1rem;
  font-size: 0.875rem;
  max-height: 400px;
  overflow-y: auto;
}

.code-block code {
  background-color: transparent;
  padding: 0;
  color: #212529;
  word-break: break-all;
  white-space: pre-wrap;
}
</style>
