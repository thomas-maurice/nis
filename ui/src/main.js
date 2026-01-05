import { createApp } from 'vue'
import { createPinia } from 'pinia'
import piniaPluginPersistedstate from 'pinia-plugin-persistedstate'
import router from './router'
import App from './App.vue'

// Bootstrap CSS
import 'bootstrap/dist/css/bootstrap.min.css'
import 'bootstrap/dist/js/bootstrap.bundle.min.js'

// Font Awesome
import { library } from '@fortawesome/fontawesome-svg-core'
import { FontAwesomeIcon } from '@fortawesome/vue-fontawesome'
import {
  faServer,
  faUsers,
  faUser,
  faNetworkWired,
  faKey,
  faSignOutAlt,
  faPlus,
  faEdit,
  faTrash,
  faCopy,
  faDownload,
  faSync,
  faHome,
  faBars,
  faChartLine
} from '@fortawesome/free-solid-svg-icons'

library.add(
  faServer,
  faUsers,
  faUser,
  faNetworkWired,
  faKey,
  faSignOutAlt,
  faPlus,
  faEdit,
  faTrash,
  faCopy,
  faDownload,
  faSync,
  faHome,
  faBars,
  faChartLine
)

const app = createApp(App)

const pinia = createPinia()
pinia.use(piniaPluginPersistedstate)

app.use(pinia)
app.use(router)
app.component('font-awesome-icon', FontAwesomeIcon)

app.mount('#app')
