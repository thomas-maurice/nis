import { defineStore } from 'pinia'
import { jwtDecode } from 'jwt-decode'

export const useAuthStore = defineStore('auth', {
  state: () => ({
    token: '',
    username: '',
    role: '',
    isAdmin: false,
    loggedIn: false,
    decoded: null
  }),

  getters: {
    isOperatorAdmin: (state) => state.role === 'operator-admin',
    isAccountAdmin: (state) => state.role === 'account-admin'
  },

  actions: {
    login(token) {
      // First clear everything to ensure clean state
      this.logout()

      this.token = token
      this.loggedIn = true

      try {
        this.decoded = jwtDecode(token)
        // Extract username and role from JWT claims if present
        if (this.decoded.username) {
          this.username = this.decoded.username
        }
        if (this.decoded.role) {
          this.role = this.decoded.role
          this.isAdmin = this.decoded.role === 'admin'
        } else {
          // Ensure role is cleared if not in token
          this.role = ''
          this.isAdmin = false
        }
      } catch (error) {
        console.error('Failed to decode JWT:', error)
        // On decode error, clear everything
        this.logout()
      }
    },

    logout() {
      this.$reset()
    },

    checkAuth() {
      return this.loggedIn && this.token !== ''
    }
  },

  persist: {
    storage: localStorage,
    paths: ['token', 'username', 'role', 'isAdmin', 'loggedIn', 'decoded']
  }
})
