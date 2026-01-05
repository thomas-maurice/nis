import { createPromiseClient } from '@connectrpc/connect'
import { createConnectTransport } from '@connectrpc/connect-web'
import { useAuthStore } from '@/stores/auth'

// Determine API base URL
const API_BASE_URL = import.meta.env.DEV
  ? 'http://localhost:8080'
  : window.location.origin

// Create Connect transport with auth interceptor
function createAuthTransport() {
  return createConnectTransport({
    baseUrl: API_BASE_URL,
    interceptors: [
      (next) => async (req) => {
        const authStore = useAuthStore()
        if (authStore.token) {
          req.header.set('Authorization', `Bearer ${authStore.token}`)
        }
        return await next(req)
      }
    ]
  })
}

// Export function to create clients
export function createClient(service) {
  return createPromiseClient(service, createAuthTransport())
}
