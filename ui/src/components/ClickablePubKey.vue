<template>
  <code
    :class="['clickable-pubkey', truncate ? 'truncated' : 'full']"
    @click="copyToClipboard"
    :title="copied ? 'Copied!' : 'Click to copy'"
  >
    {{ displayKey }}
    <font-awesome-icon
      v-if="copied"
      :icon="['fas', 'check']"
      class="ms-1 text-success"
    />
  </code>
</template>

<script setup>
import { ref, computed } from 'vue'

const props = defineProps({
  pubkey: {
    type: String,
    required: true
  },
  truncate: {
    type: Boolean,
    default: false
  },
  truncateLength: {
    type: Number,
    default: 20
  }
})

const copied = ref(false)

const displayKey = computed(() => {
  if (props.truncate && props.pubkey.length > props.truncateLength) {
    return props.pubkey.substring(0, props.truncateLength) + '...'
  }
  return props.pubkey
})

const copyToClipboard = async () => {
  try {
    await navigator.clipboard.writeText(props.pubkey)
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
.clickable-pubkey {
  cursor: pointer;
  user-select: none;
  transition: background-color 0.2s;
  padding: 2px 6px;
  border-radius: 4px;
}

.clickable-pubkey:hover {
  background-color: rgba(13, 110, 253, 0.1);
}

.clickable-pubkey.full {
  word-break: break-all;
}

.clickable-pubkey.truncated {
  display: inline-block;
}
</style>
