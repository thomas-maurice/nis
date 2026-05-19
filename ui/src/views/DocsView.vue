<template>
  <div class="container py-4">
    <div class="d-flex align-items-center mb-4">
      <font-awesome-icon :icon="['fas', 'book']" class="text-primary me-3" size="lg" />
      <h1 class="mb-0">Documentation</h1>
    </div>
    <p class="text-muted mb-4">
      How to talk to this NIS server: API access, the <code>nisctl</code> CLI, and
      the YAML manifest format for declarative apply.
    </p>

    <!-- API Access -->
    <div class="card mb-4">
      <div class="card-body">
        <h5 class="card-title">
          <font-awesome-icon :icon="['fas', 'flask']" class="text-secondary me-2" />
          API access
        </h5>
        <p class="card-text">
          NIS exposes a Connect-RPC API (proto over HTTP) with gRPC server reflection
          enabled. Any Connect or gRPC client can talk to it.
        </p>

        <h6 class="mt-3">Server URL</h6>
        <CodeBlock :content="serverHost" label="" :can-copy="true" />

        <h6 class="mt-3">List services with grpcurl</h6>
        <CodeBlock :content="`grpcurl -plaintext ${serverHost} list`" />

        <h6 class="mt-3">Describe a service</h6>
        <CodeBlock :content="`grpcurl -plaintext ${serverHost} describe nis.v1.OperatorService`" />

        <h6 class="mt-3">GUI clients</h6>
        <p class="small text-muted mb-2">
          <strong>Bruno</strong>, <strong>Postman</strong>, and <strong>Kreya</strong>
          can import the API automatically via reflection. Point them at
          <code>{{ serverHost }}</code> and pick a service.
        </p>

        <h6 class="mt-3">Authentication</h6>
        <p class="small text-muted mb-2">
          Every request (except <code>AuthService/Login</code>) needs an
          <code>Authorization: Bearer &lt;token&gt;</code> header. Two ways to get a token:
        </p>
        <ul class="small text-muted">
          <li>
            <strong>Username + password</strong> — short-lived JWT from
            <code>AuthService/Login</code>. <code>nisctl login</code> does this for you.
          </li>
          <li>
            <strong>API token</strong> — long-lived opaque token (<code>nis_pat_…</code>)
            created via <router-link to="/api-tokens">API Tokens</router-link>. Use for CI,
            service accounts, automation.
          </li>
        </ul>
      </div>
    </div>

    <!-- nisctl CLI -->
    <div class="card mb-4">
      <div class="card-body">
        <h5 class="card-title">
          <font-awesome-icon :icon="['fas', 'terminal']" class="text-secondary me-2" />
          The <code>nisctl</code> CLI
        </h5>
        <p class="card-text">
          <code>nisctl</code> is the command-line client for this server. It owns
          operator/account/user CRUD, cluster sync, backup/restore, and the bulk
          manifest workflow described below.
        </p>

        <h6 class="mt-3">Build it</h6>
        <CodeBlock content="make build-cli   # produces ./bin/nisctl" />

        <h6 class="mt-3">Log in</h6>
        <CodeBlock
          :content="`nisctl login ${serverURL} -u USERNAME -p PASSWORD`"
        />
        <p class="small text-muted mt-2 mb-0">
          The session token is stored at
          <code>~/.config/nisctl/config.yaml</code> and used on every subsequent
          command. <code>nisctl logout</code> clears it.
        </p>

        <h6 class="mt-3">Or skip login with an API token</h6>
        <CodeBlock content='export NIS_TOKEN="nis_pat_..."   # then any nisctl command works' />
        <p class="small text-muted mt-2 mb-0">
          Precedence: <code>--token</code> flag &gt; <code>NIS_TOKEN</code> env &gt;
          stored session. Useful in CI where interactive login isn't possible.
        </p>

        <h6 class="mt-3">Common commands</h6>
        <CodeBlock :content="commonCommands" />

        <h6 class="mt-3">Discover more</h6>
        <CodeBlock content="nisctl --help          # top-level commands
nisctl operator --help # subcommand help
nisctl apply --help    # flag reference for any command" />
      </div>
    </div>

    <!-- Manifests -->
    <div class="card mb-4">
      <div class="card-body">
        <h5 class="card-title">
          <font-awesome-icon :icon="['fas', 'file-import']" class="text-secondary me-2" />
          YAML manifests (declarative apply)
        </h5>
        <p class="card-text">
          Describe operators, accounts, users, scoped signing keys, and clusters
          as YAML — then apply the whole tree with one command. Kubectl-style
          multi-document format: one <code>kind</code> per doc, separated by
          <code>---</code>. <code>metadata</code> holds identity (name + parent
          refs); <code>spec</code> holds the desired state.
        </p>

        <h6 class="mt-3">The five kinds</h6>
        <ul class="small mb-3">
          <li><code>Operator</code> — top of the trust hierarchy. Globally-unique name.</li>
          <li><code>Cluster</code> — a NATS cluster bound to one operator.</li>
          <li><code>Account</code> — tenant within an operator. Unique within its operator.</li>
          <li><code>ScopedSigningKey</code> — a permission template under an account. Unique within its account.</li>
          <li><code>User</code> — a NATS identity that hangs off an account, optionally signed by a scoped key.</li>
        </ul>

        <h6 class="mt-3">Workflow</h6>
        <ol class="small">
          <li><code>nisctl dump operator NAME &gt; op.yaml</code> — snapshot current state as a manifest.</li>
          <li>Edit <code>op.yaml</code> in your editor.</li>
          <li><code>nisctl diff -f op.yaml</code> — preview what would change.</li>
          <li><code>nisctl apply -f op.yaml</code> — reconcile (prompts for confirmation; <code>-y</code> to skip).</li>
        </ol>

        <h6 class="mt-3">Commands</h6>
        <CodeBlock :content="manifestCommands" />

        <div class="alert alert-warning small mt-3 mb-3" role="alert">
          <strong>Reserved names:</strong>
          <code>$SYS</code> account and <code>system</code> user are auto-created
          per operator and cannot be declared or deleted via manifest.
          The <code>default</code> scoped key is auto-created per account but
          <em>can</em> be declared to customize its permissions; manifest delete
          refuses it.
          <br><br>
          <strong>v1 limitations:</strong>
          cluster updates via manifest are not supported (use <code>nisctl cluster update</code>).
          User scoped-key reassignment is not supported (would invalidate
          existing creds); do explicit delete + recreate.
        </div>

        <h6 class="mt-4">Example manifests</h6>
        <p class="small text-muted">
          One file per kind, plus a full-stack example. These are pulled directly
          from <code>example/manifests/</code> in the repo — single source of truth.
        </p>

        <ul class="nav nav-tabs mt-3" role="tablist">
          <li v-for="(ex, idx) in examples" :key="ex.id" class="nav-item" role="presentation">
            <button
              class="nav-link"
              :class="{ active: activeTab === ex.id }"
              type="button"
              role="tab"
              @click="activeTab = ex.id"
            >
              {{ ex.label }}
            </button>
          </li>
        </ul>

        <div class="tab-content border border-top-0 p-3">
          <div
            v-for="ex in examples"
            :key="ex.id"
            v-show="activeTab === ex.id"
            class="tab-pane"
            :class="{ active: activeTab === ex.id }"
          >
            <p class="small text-muted mb-2">{{ ex.description }}</p>
            <CodeBlock :content="ex.content" />
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'
import CodeBlock from '@/components/CodeBlock.vue'

import operatorYaml from '../../../example/manifests/operator.yaml?raw'
import clusterYaml from '../../../example/manifests/cluster.yaml?raw'
import accountYaml from '../../../example/manifests/account.yaml?raw'
import scopedKeyYaml from '../../../example/manifests/scoped-signing-key.yaml?raw'
import userYaml from '../../../example/manifests/user.yaml?raw'
import fullStackYaml from '../../../example/manifests/full-stack.yaml?raw'

const serverHost = computed(() => {
  if (typeof window === 'undefined') return 'localhost:8080'
  return window.location.host
})

const serverURL = computed(() => {
  if (typeof window === 'undefined') return 'http://localhost:8080'
  return `${window.location.protocol}//${window.location.host}`
})

const commonCommands = computed(() => `nisctl operator list
nisctl account list OPERATOR_NAME
nisctl user list ACCOUNT_NAME
nisctl user creds USER_NAME --operator OP --account ACC > my.creds
nisctl cluster sync CLUSTER_NAME`)

const manifestCommands = `nisctl apply  -f file.yaml [--dry-run] [-y]   # create/update entities
nisctl diff   -f file.yaml                      # dry-run (alias for apply --dry-run)
nisctl delete -f file.yaml [-y]                 # tear down entities in reverse topo order
nisctl dump operator NAME [-o file.yaml]        # snapshot current state as a manifest`

const examples = [
  {
    id: 'full-stack',
    label: 'Full stack',
    description:
      'A complete multi-document manifest for an "ACME" operator: cluster, two accounts, scoped keys, users. Apply this to bring up the full tree in one command.',
    content: fullStackYaml,
  },
  {
    id: 'operator',
    label: 'Operator',
    description: 'A single Operator with an optional JWT lifecycle policy (TTL, warn window, auto-renew).',
    content: operatorYaml,
  },
  {
    id: 'cluster',
    label: 'Cluster',
    description: 'A NATS cluster bound to an operator. Apply creates it; update its URLs via nisctl cluster update.',
    content: clusterYaml,
  },
  {
    id: 'account',
    label: 'Account',
    description: 'An account under an operator, optionally with JetStream limits.',
    content: accountYaml,
  },
  {
    id: 'scoped-signing-key',
    label: 'ScopedSigningKey',
    description: 'A permission template that users can be signed by. Permissions are pub/sub allow/deny lists.',
    content: scopedKeyYaml,
  },
  {
    id: 'user',
    label: 'User',
    description: 'A NATS user under an account. Optionally references a scoped key by name for permissions; optional per-user JWT TTL override.',
    content: userYaml,
  },
]

const activeTab = ref('full-stack')
</script>

<style scoped>
.nav-tabs .nav-link {
  cursor: pointer;
}
h6 {
  font-weight: 600;
  margin-top: 0.5rem;
}
</style>
