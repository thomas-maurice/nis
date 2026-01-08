<template>
  <div id="app" class="d-flex flex-column min-vh-100">
    <NavBar v-if="authStore.checkAuth()" />
    <main class="flex-grow-1">
      <router-view />
    </main>
    <footer v-if="authStore.checkAuth()" class="py-3 mt-auto">
      <div class="container text-center text-muted">
        <small>NATS Identity Service &copy; 2026</small>
      </div>
    </footer>
  </div>
</template>

<script setup>
import { useAuthStore } from '@/stores/auth'
import NavBar from '@/components/NavBar.vue'

const authStore = useAuthStore()
</script>

<style>
:root {
  --bs-primary: #375a7f;
  --bs-secondary: #444;
  --bs-success: #00bc8c;
  --bs-danger: #e74c3c;
  --bs-warning: #f39c12;
  --bs-info: #3498db;
}

body {
  font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif;
  background-color: #f8f9fa;
}

.navbar {
  box-shadow: 0 2px 4px rgba(0, 0, 0, 0.1);
}

.card {
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.12);
  border: none;
  margin-bottom: 1rem;
}

.btn {
  font-weight: 500;
}

.table {
  background-color: white;
}

pre {
  background-color: #f4f4f4;
  border: 1px solid #ddd;
  border-radius: 4px;
  padding: 1rem;
  overflow-x: auto;
}

code {
  background-color: #f4f4f4;
  padding: 2px 6px;
  border-radius: 3px;
  font-size: 90%;
}

/* Footer styling for light mode */
footer {
  background-color: #f8f9fa;
  border-top: 1px solid #dee2e6;
}
</style>
