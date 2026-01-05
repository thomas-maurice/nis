import { defineStore } from 'pinia'
import { jwtDecode } from 'jwt-decode'

export const useAuthStore = defineStore('auth', {
  state: () => ({
    token: '',
    username: '',
    isAdmin: false,
    loggedIn: false,
    decoded: null
  }),

  actions: {
    login(token) {
      this.token = token
      this.loggedIn = true

      try {
        this.decoded = jwtDecode(token)
        // Extract username and admin status from JWT claims if present
        if (this.decoded.username) {
          this.username = this.decoded.username
        }
        if (this.decoded.role === 'admin') {
          this.isAdmin = true
        }
      } catch (error) {
        console.error('Failed to decode JWT:', error)
      }
    },

    logout() {
      this.token = ''
      this.username = ''
      this.isAdmin = false
      this.loggedIn = false
      this.decoded = null
    },

    checkAuth() {
      return this.loggedIn && this.token !== ''
    }
  },

  persist: {
    storage: localStorage,
    paths: ['token', 'username', 'isAdmin', 'loggedIn', 'decoded']
  }
})
