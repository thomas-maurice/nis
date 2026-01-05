<template>
  <div class="modal fade show d-block" tabindex="-1" role="dialog" style="background-color: rgba(0,0,0,0.5);">
    <div class="modal-dialog modal-lg" role="document">
      <div class="modal-content">
        <div class="modal-header">
          <h5 class="modal-title">{{ title }}</h5>
          <button type="button" class="btn-close" @click="$emit('close')"></button>
        </div>
        <form @submit.prevent="$emit('submit', formData)">
          <div class="modal-body">
            <div v-if="error" class="alert alert-danger">
              {{ error }}
            </div>

            <slot name="fields" :formData="formData"></slot>
          </div>
          <div class="modal-footer">
            <button type="button" class="btn btn-secondary" @click="$emit('close')" :disabled="loading">
              Cancel
            </button>
            <button type="submit" class="btn btn-primary" :disabled="loading">
              <span v-if="loading" class="spinner-border spinner-border-sm me-2"></span>
              {{ submitLabel }}
            </button>
          </div>
        </form>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, watch } from 'vue'

const props = defineProps({
  title: {
    type: String,
    required: true
  },
  submitLabel: {
    type: String,
    default: 'Save'
  },
  initialData: {
    type: Object,
    default: () => ({})
  },
  loading: {
    type: Boolean,
    default: false
  },
  error: {
    type: String,
    default: ''
  }
})

defineEmits(['submit', 'close'])

const formData = ref({ ...props.initialData })

watch(
  () => props.initialData,
  (newData) => {
    formData.value = { ...newData }
  },
  { deep: true }
)
</script>

<style scoped>
.modal {
  display: block;
}

.modal-dialog {
  margin-top: 3rem;
}
</style>
