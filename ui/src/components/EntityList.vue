<template>
  <div class="entity-list">
    <div class="d-flex justify-content-between align-items-center mb-3">
      <h2>{{ title }}</h2>
      <div>
        <slot name="header-actions"></slot>
        <button v-if="canCreate" class="btn btn-primary" @click="$emit('create')">
          <font-awesome-icon :icon="['fas', 'plus']" class="me-2" />
          Create {{ entityName }}
        </button>
      </div>
    </div>

    <div v-if="loading" class="text-center py-5">
      <div class="spinner-border text-primary" role="status">
        <span class="visually-hidden">Loading...</span>
      </div>
    </div>

    <div v-else-if="error" class="alert alert-danger">
      {{ error }}
    </div>

    <div v-else-if="items.length === 0" class="alert alert-info">
      No {{ title.toLowerCase() }} found. Click "Create {{ entityName }}" to get started.
    </div>

    <div v-else class="card">
      <div class="table-responsive">
        <table class="table table-hover mb-0">
          <thead>
            <tr>
              <th v-for="column in columns" :key="column.key">
                {{ column.label }}
              </th>
              <th class="text-end">Actions</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="item in items" :key="item.id" @click="$emit('select', item)" style="cursor: pointer;">
              <td v-for="column in columns" :key="column.key">
                <slot :name="`cell-${column.key}`" :item="item">
                  {{ getValue(item, column.key) }}
                </slot>
              </td>
              <td class="text-end" @click.stop>
                <div class="btn-group btn-group-sm">
                  <slot name="custom-actions" :item="item"></slot>
                  <button
                    class="btn btn-outline-primary"
                    @click="$emit('edit', item)"
                    title="Edit"
                  >
                    <font-awesome-icon :icon="['fas', 'edit']" />
                  </button>
                  <button
                    v-if="canDelete"
                    class="btn btn-outline-danger"
                    @click="$emit('delete', item)"
                    title="Delete"
                  >
                    <font-awesome-icon :icon="['fas', 'trash']" />
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </div>
</template>

<script setup>
defineProps({
  title: {
    type: String,
    required: true
  },
  entityName: {
    type: String,
    required: true
  },
  items: {
    type: Array,
    default: () => []
  },
  columns: {
    type: Array,
    required: true
  },
  loading: {
    type: Boolean,
    default: false
  },
  error: {
    type: String,
    default: ''
  },
  canCreate: {
    type: Boolean,
    default: true
  },
  canDelete: {
    type: Boolean,
    default: true
  }
})

defineEmits(['create', 'edit', 'delete', 'select'])

const getValue = (item, key) => {
  const keys = key.split('.')
  let value = item
  for (const k of keys) {
    value = value?.[k]
  }
  return value || '-'
}
</script>

<style scoped>
.entity-list h2 {
  margin: 0;
  font-size: 1.75rem;
  font-weight: 600;
}

.table tbody tr:hover {
  background-color: rgba(0, 0, 0, 0.025);
}

.btn-group-sm .btn {
  padding: 0.25rem 0.5rem;
  font-size: 0.875rem;
}
</style>
