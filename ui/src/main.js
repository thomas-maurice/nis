import { createApp } from 'vue'
import { createPinia } from 'pinia'
import piniaPluginPersistedstate from 'pinia-plugin-persistedstate'
import router from './router'
import App from './App.vue'

// Bootstrap CSS
import 'bootstrap/dist/css/bootstrap.min.css'
import 'bootstrap/dist/js/bootstrap.bundle.min.js'

// Dark mode CSS
import './assets/darkmode.css'

// Font Awesome
import { library } from '@fortawesome/fontawesome-svg-core'
import { FontAwesomeIcon } from '@fortawesome/vue-fontawesome'
import {
  faServer,
  faUsers,
  faUser,
  faNetworkWired,
  faKey,
  faEye,
  faSignOutAlt,
  faPlus,
  faEdit,
  faTrash,
  faCopy,
  faDownload,
  faSync,
  faHome,
  faBars,
  faChartLine,
  faUserGear,
  faFlask,
  faUserShield,
  faListAlt,
  faTowerBroadcast,
  faPaperPlane,
  faExclamationTriangle,
  faCheck,
  faCheckCircle,
  faQuestionCircle,
  faShieldAlt,
  faFileExport,
  faFileImport,
  faArrowUpRightFromSquare,
  faBan,
  faBook,
  faTerminal,
  faMagnifyingGlass,
  faXmark,
  faSpinner,
  faLayerGroup,
  faLink,
  faLinkSlash,
  faCircleExclamation,
  faRotate,
  faClock,
  faCloudArrowUp,
  faCloudArrowDown,
} from '@fortawesome/free-solid-svg-icons'

library.add(
  faServer,
  faUsers,
  faUser,
  faNetworkWired,
  faKey,
  faEye,
  faSignOutAlt,
  faPlus,
  faEdit,
  faTrash,
  faCopy,
  faDownload,
  faSync,
  faHome,
  faBars,
  faChartLine,
  faUserGear,
  faFlask,
  faUserShield,
  faListAlt,
  faTowerBroadcast,
  faPaperPlane,
  faExclamationTriangle,
  faCheck,
  faCheckCircle,
  faQuestionCircle,
  faShieldAlt,
  faFileExport,
  faFileImport,
  faArrowUpRightFromSquare,
  faBan,
  faBook,
  faTerminal,
  faMagnifyingGlass,
  faXmark,
  faSpinner,
  faLayerGroup,
  faLink,
  faLinkSlash,
  faCircleExclamation,
  faRotate,
  faClock,
  faCloudArrowUp,
  faCloudArrowDown
)

const app = createApp(App)

const pinia = createPinia()
pinia.use(piniaPluginPersistedstate)

app.use(pinia)
app.use(router)
app.component('font-awesome-icon', FontAwesomeIcon)

app.mount('#app')
