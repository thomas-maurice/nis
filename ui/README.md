# NIS Web UI

Modern Vue.js web interface for the NATS Identity Service.

## Features

- 🔐 Authentication with JWT
- 📊 Dashboard with statistics
- 🔧 Full CRUD operations for:
  - Operators
  - Accounts
  - Users
  - Clusters
- 💾 Automatic state persistence
- 📱 Responsive design with Bootstrap 5
- 🎨 Clean, professional interface

## Development Setup

### Prerequisites

- Node.js 18+ and npm
- Running NIS server backend

### Install Dependencies

```bash
npm install
```

### Development Server

Start the Vite dev server with hot module replacement:

```bash
npm run dev
```

The UI will be available at `http://localhost:5173` and will proxy API requests to `http://localhost:8080`.

### Build for Production

```bash
npm run build
```

This creates optimized production files in the `dist/` directory, which are embedded into the Go binary.

## Project Structure

```
ui/
├── src/
│   ├── components/      # Reusable Vue components
│   │   ├── NavBar.vue
│   │   ├── AuthGuard.vue
│   │   ├── EntityList.vue
│   │   ├── EntityForm.vue
│   │   └── CodeBlock.vue
│   ├── views/           # Page-level components
│   │   ├── LoginView.vue
│   │   ├── DashboardView.vue
│   │   ├── OperatorsView.vue
│   │   ├── AccountsView.vue
│   │   ├── UsersView.vue
│   │   └── ClustersView.vue
│   ├── stores/          # Pinia state stores
│   │   └── auth.js
│   ├── router/          # Vue Router configuration
│   │   └── index.js
│   ├── utils/           # Utility functions
│   │   ├── api.js       # Axios client
│   │   └── connect.js   # Connect-RPC client
│   ├── App.vue          # Root component
│   └── main.js          # Application entry point
├── public/              # Static assets
├── index.html           # HTML template
├── vite.config.js       # Vite configuration
└── package.json         # Dependencies and scripts
```

## API Integration

The UI communicates with the NIS backend using Connect-RPC over HTTP. All API calls are automatically authenticated using JWT tokens stored in localStorage.

### Authentication Flow

1. User logs in via `/login`
2. Backend returns JWT token
3. Token stored in Pinia store (persisted to localStorage)
4. Token automatically sent with all API requests via Axios interceptor
5. On 401 response, user is redirected to login

## Component Architecture

### Reusable Components

- **EntityList**: Generic list view with create/edit/delete actions
- **EntityForm**: Modal form component with validation
- **CodeBlock**: Syntax-highlighted code display with copy functionality
- **NavBar**: Main navigation with authentication state
- **AuthGuard**: Session validation component

### Views

Each entity type (Operators, Accounts, Users, Clusters) has:
- List view (e.g., `OperatorsView.vue`)
- Detail view (e.g., `OperatorDetailView.vue`)

## Technologies

- **Vue 3**: Progressive JavaScript framework
- **Vite**: Fast build tool and dev server
- **Pinia**: State management
- **Vue Router**: Client-side routing
- **Bootstrap 5**: UI framework
- **Font Awesome**: Icons
- **Axios**: HTTP client
- **JWT Decode**: Token parsing

## Environment Variables

The UI automatically detects the environment:

- **Development**: API calls proxy to `http://localhost:8080`
- **Production**: API calls use the same origin as the UI

## Build Integration

The production build is embedded into the Go binary:

1. `npm run build` creates `dist/` folder
2. Makefile copies `dist/` to `internal/interfaces/http/ui/dist/`
3. Go `embed` directive includes these files in the binary
4. Server serves UI at `/` and API at `/nis.v1/*`
