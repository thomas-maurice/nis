<template>
  <div class="container-fluid py-4">
    <div v-if="loading" class="text-center py-5">
      <div class="spinner-border text-primary" role="status"></div>
    </div>

    <div v-else-if="error" class="alert alert-danger">{{ error }}</div>

    <div v-else-if="operator">
      <div class="d-flex justify-content-between align-items-center mb-4">
        <h1>{{ operator.name }}</h1>
        <div>
          <button v-if="authStore.isAdmin || authStore.isOperatorAdmin" class="btn btn-outline-success me-2" @click="showExportModal = true">
            <font-awesome-icon :icon="['fas', 'file-export']" class="me-2" />
            Backup
          </button>
          <router-link to="/operators" class="btn btn-outline-secondary">
            Back to Operators
          </router-link>
        </div>
      </div>

      <div class="row g-4">
        <div class="col-md-6">
          <div class="card">
            <div class="card-header">
              <h5 class="mb-0">Operator Details</h5>
            </div>
            <div class="card-body">
              <dl class="row mb-0">
                <dt class="col-sm-4">ID:</dt>
                <dd class="col-sm-8"><code>{{ operator.id }}</code></dd>

                <dt class="col-sm-4">Name:</dt>
                <dd class="col-sm-8">{{ operator.name }}</dd>

                <dt class="col-sm-4">Description:</dt>
                <dd class="col-sm-8">{{ operator.description || '-' }}</dd>

                <dt class="col-sm-4">Public Key:</dt>
                <dd class="col-sm-8"><ClickablePubKey :pubkey="operator.publicKey" /></dd>

                <dt class="col-sm-4">System Account:</dt>
                <dd class="col-sm-8">
                  <ClickablePubKey v-if="operator.systemAccountPubKey" :pubkey="operator.systemAccountPubKey" />
                  <span v-else class="text-muted">Not set</span>
                </dd>

                <template v-if="authStore.isAdmin || authStore.isOperatorAdmin">
                  <dt class="col-sm-4">NIS Admin User:</dt>
                  <dd class="col-sm-8">
                    <span v-if="hasAdminAccount" class="text-success">
                      <font-awesome-icon :icon="['fas', 'check-circle']" class="me-1" />
                      Configured
                    </span>
                    <span v-else-if="checkingAdminAccount" class="text-muted">
                      <span class="spinner-border spinner-border-sm me-1"></span>
                      Checking...
                    </span>
                    <div v-else>
                      <span class="text-warning me-2">
                        <font-awesome-icon :icon="['fas', 'exclamation-triangle']" class="me-1" />
                        Not configured
                      </span>
                      <button class="btn btn-sm btn-primary" @click="createAdminAccount" :disabled="creatingAdminAccount">
                        <span v-if="creatingAdminAccount" class="spinner-border spinner-border-sm me-1"></span>
                        Create Admin User
                      </button>
                    </div>
                    <div v-if="adminAccountError" class="text-danger small mt-1">{{ adminAccountError }}</div>
                  </dd>
                </template>

                <dt class="col-sm-4">Created:</dt>
                <dd class="col-sm-8">{{ formatDate(operator.createdAt) }}</dd>
              </dl>
            </div>
          </div>

          <div v-if="clusters.length > 0" class="card mt-3">
            <div class="card-header">
              <h5 class="mb-0">Clusters</h5>
            </div>
            <div class="card-body">
              <ul class="list-group">
                <li
                  v-for="cluster in clusters"
                  :key="cluster.id"
                  class="list-group-item d-flex justify-content-between align-items-center"
                >
                  <router-link :to="`/clusters/${cluster.id}`">{{ cluster.name }}</router-link>
                  <span :class="cluster.healthy ? 'badge bg-success' : 'badge bg-secondary'">
                    {{ cluster.healthy ? 'Healthy' : 'Unknown' }}
                  </span>
                </li>
              </ul>
            </div>
          </div>
        </div>

        <div class="col-md-6">
          <div class="card">
            <div class="card-header">
              <h5 class="mb-0">Operator JWT</h5>
            </div>
            <div class="card-body">
              <CodeBlock :content="operator.jwt" label="" />
            </div>
          </div>
        </div>
      </div>

      <!-- JWT Policy Card -->
      <div class="row mt-4">
        <div class="col-md-6">
          <div class="card">
            <div class="card-header d-flex justify-content-between align-items-center">
              <h5 class="mb-0">JWT Policy</h5>
              <button
                v-if="authStore.isAdmin || authStore.isOperatorAdmin"
                class="btn btn-sm btn-outline-primary"
                @click="openJWTPolicyModal"
              >
                <font-awesome-icon :icon="['fas', 'edit']" class="me-1" />
                Edit
              </button>
            </div>
            <div class="card-body">
              <dl class="row mb-0">
                <dt class="col-sm-6">User JWT TTL:</dt>
                <dd class="col-sm-6">{{ formatTTLDays(operator.userJwtTtlSeconds) }}</dd>

                <dt class="col-sm-6">Account JWT TTL:</dt>
                <dd class="col-sm-6">{{ formatTTLDays(operator.accountJwtTtlSeconds) }}</dd>

                <dt class="col-sm-6">Warn Window:</dt>
                <dd class="col-sm-6">{{ formatTTLDays(operator.jwtWarnWindowSeconds) }}</dd>

                <dt class="col-sm-6">Auto-Renew:</dt>
                <dd class="col-sm-6">
                  <span v-if="operator.jwtAutoRenew" class="badge bg-success">Yes</span>
                  <span v-else class="badge bg-secondary">Off</span>
                </dd>
              </dl>
            </div>
          </div>
        </div>

        <!-- Admin Tools -->
        <div v-if="authStore.isAdmin" class="col-md-6">
          <div class="card">
            <div class="card-header">
              <h5 class="mb-0">Admin Tools</h5>
            </div>
            <div class="card-body">
              <p class="text-muted small mb-3">
                Force an immediate JWT expiry sweep across all operators. Normally runs on a periodic tick.
              </p>
              <button
                class="btn btn-outline-warning"
                :disabled="sweepRunning"
                @click="runSweep"
              >
                <span v-if="sweepRunning" class="spinner-border spinner-border-sm me-2"></span>
                <font-awesome-icon v-else :icon="['fas', 'sync']" class="me-2" />
                Run JWT Expiry Sweep Now
              </button>
              <div v-if="sweepResult" class="alert alert-info mt-3 mb-0">
                <strong>Sweep complete:</strong>
                pruned {{ sweepResult.revocationsPruned }} revocation(s),
                {{ sweepResult.expiringSoonEmitted }} expiring-soon event(s),
                {{ sweepResult.expiredAlertsEmitted }} expired alert(s),
                {{ sweepResult.autoRenewed }} auto-renewed.
              </div>
              <div v-if="sweepError" class="alert alert-danger mt-3 mb-0">{{ sweepError }}</div>
            </div>
          </div>
        </div>
      </div>

      <!-- Backups Card -->
      <div v-if="authStore.isAdmin || authStore.isOperatorAdmin" class="row mt-4">
        <div class="col-12">
          <div class="card">
            <div class="card-header d-flex justify-content-between align-items-center">
              <h5 class="mb-0">
                <font-awesome-icon :icon="['fas', 'cloud-arrow-up']" class="me-2" />
                Backups
              </h5>
            </div>
            <div class="card-body">
              <!-- NIS-wide disabled sentinel alert -->
              <div v-if="backupsGloballyDisabled" class="alert alert-warning mb-3">
                Scheduled backups are disabled at the NIS-wide level. Ask an administrator to enable
                <code>BACKUPS_ENABLED</code> in the NIS server config.
              </div>

              <!-- Settings summary -->
              <div class="mb-3">
                <span v-if="backupSettings && backupSettings.enabled" class="badge bg-success me-2">Enabled</span>
                <span v-else class="badge bg-secondary me-2">Disabled</span>
                <template v-if="backupSettings && backupSettings.enabled">
                  Interval: {{ formatIntervalSeconds(backupSettings.intervalSeconds) }}
                  &bull; Retention: {{ backupSettings.retentionCount ? backupSettings.retentionCount : 'unlimited' }}
                  &bull; Last backup:
                  <span v-if="backupSettings.lastBackupAt">{{ formatDate(backupSettings.lastBackupAt.toDate()) }}</span>
                  <span v-else class="text-muted">never</span>
                </template>
              </div>

              <!-- Action buttons -->
              <div class="d-flex gap-2 mb-3">
                <button
                  class="btn btn-outline-primary btn-sm"
                  :disabled="backupsGloballyDisabled"
                  @click="openBackupConfigModal"
                >
                  <font-awesome-icon :icon="['fas', 'edit']" class="me-1" />
                  Configure
                </button>
                <button
                  class="btn btn-outline-success btn-sm"
                  :disabled="backupsGloballyDisabled || runningBackup"
                  @click="runBackupNow"
                >
                  <span v-if="runningBackup" class="spinner-border spinner-border-sm me-1"></span>
                  <font-awesome-icon v-else :icon="['fas', 'cloud-arrow-up']" class="me-1" />
                  Run now
                </button>
                <button
                  class="btn btn-outline-secondary btn-sm"
                  :disabled="loadingBackups"
                  @click="loadBackups"
                >
                  <span v-if="loadingBackups" class="spinner-border spinner-border-sm me-1"></span>
                  <font-awesome-icon v-else :icon="['fas', 'sync']" class="me-1" />
                  Refresh
                </button>
              </div>

              <div v-if="backupError" class="alert alert-danger mb-3">{{ backupError }}</div>

              <!-- Backups table -->
              <div v-if="backups.length === 0 && !loadingBackups" class="text-muted small">No backups yet.</div>
              <div v-else class="table-responsive">
                <table class="table table-sm table-hover mb-0">
                  <thead>
                    <tr>
                      <th>Created</th>
                      <th>Size</th>
                      <th>Trigger</th>
                      <th>SHA256</th>
                      <th>Object key</th>
                      <th class="text-end">Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr v-for="bk in backups" :key="bk.id">
                      <td>{{ bk.createdAt ? formatDate(bk.createdAt.toDate()) : '-' }}</td>
                      <td>{{ formatBytes(bk.sizeBytes) }}</td>
                      <td>
                        <span :class="bk.triggerKind === 'scheduled' ? 'badge bg-secondary' : 'badge bg-info text-dark'">
                          {{ bk.triggerKind || 'unknown' }}
                        </span>
                      </td>
                      <td><code class="text-break">{{ bk.sha256 || '-' }}</code></td>
                      <td><code class="text-break">{{ bk.objectKey }}</code></td>
                      <td class="text-end" style="white-space: nowrap;">
                        <button
                          class="btn btn-sm btn-outline-primary me-1"
                          :disabled="downloadingBackupId === bk.id"
                          @click="downloadBackup(bk)"
                          title="Download"
                        >
                          <span v-if="downloadingBackupId === bk.id" class="spinner-border spinner-border-sm"></span>
                          <font-awesome-icon v-else :icon="['fas', 'cloud-arrow-down']" />
                        </button>
                        <button
                          class="btn btn-sm btn-outline-danger"
                          @click="deleteBackup(bk)"
                          title="Delete"
                        >
                          <font-awesome-icon :icon="['fas', 'trash']" />
                        </button>
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- Backup recipients (age) Card -->
      <div v-if="authStore.isAdmin || authStore.isOperatorAdmin" class="row mt-4">
        <div class="col-12">
          <div class="card">
            <div class="card-header d-flex justify-content-between align-items-center">
              <h5 class="mb-0">
                <font-awesome-icon :icon="['fas', 'key']" class="me-2" />
                Backup recipients (age)
              </h5>
              <button
                class="btn btn-outline-primary btn-sm"
                :disabled="backupsGloballyDisabled"
                @click="openAddRecipientModal"
              >
                <font-awesome-icon :icon="['fas', 'plus']" class="me-1" />
                Add recipient
              </button>
            </div>
            <div class="card-body">
              <p class="text-muted small mb-3">
                Age public keys authorised to decrypt scheduled-backup artifacts. NIS never
                sees the private half &mdash; generate keypairs locally with
                <code>nisctl backup keygen</code>. At least one recipient is required before
                any scheduled or manual backup will succeed.
              </p>

              <div
                v-if="!loadingRecipients && recipients.length === 0 && backupSettings && backupSettings.enabled"
                class="alert alert-warning mb-3"
              >
                <font-awesome-icon :icon="['fas', 'exclamation-triangle']" class="me-2" />
                No recipients registered. Scheduled backups will fail until you add at least one.
              </div>

              <div v-if="lastActiveRecipientRemoved" class="alert alert-warning mb-3">
                <font-awesome-icon :icon="['fas', 'exclamation-triangle']" class="me-2" />
                You just removed the last recipient. Scheduled backups will fail until you add one back.
              </div>

              <div v-if="recipientsError" class="alert alert-danger mb-3">{{ recipientsError }}</div>

              <div v-if="loadingRecipients" class="text-muted small">
                <span class="spinner-border spinner-border-sm me-1"></span> Loading...
              </div>
              <div v-else-if="recipients.length === 0" class="text-muted small">
                No recipients configured.
              </div>
              <div v-else class="table-responsive">
                <table class="table table-sm table-hover mb-0">
                  <thead>
                    <tr>
                      <th>Label</th>
                      <th>Public key</th>
                      <th>Added</th>
                      <th class="text-end">Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr v-for="r in recipients" :key="r.id">
                      <td>{{ r.label || '-' }}</td>
                      <td><code class="text-break">{{ r.publicKey }}</code></td>
                      <td>{{ r.createdAt ? formatDate(r.createdAt.toDate()) : '-' }}</td>
                      <td class="text-end" style="white-space: nowrap;">
                        <button
                          class="btn btn-sm btn-outline-danger"
                          :disabled="removingRecipientId === r.id"
                          @click="removeRecipient(r)"
                          title="Remove"
                        >
                          <span v-if="removingRecipientId === r.id" class="spinner-border spinner-border-sm"></span>
                          <font-awesome-icon v-else :icon="['fas', 'trash']" />
                        </button>
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </div>
          </div>
        </div>
      </div>

      <div v-if="config" class="row mt-4">
        <div class="col-12">
          <div class="card">
            <div class="card-header d-flex justify-content-between align-items-center">
              <h5 class="mb-0">NATS Server Configuration</h5>
              <button class="btn btn-sm btn-primary" @click="downloadConfig">
                <font-awesome-icon :icon="['fas', 'download']" class="me-2" />
                Download Config
              </button>
            </div>
            <div class="card-body">
              <CodeBlock :content="config" label="" />
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- JWT Policy Modal -->
    <div v-if="showJWTPolicyModal" class="modal fade show d-block" tabindex="-1" style="background-color: rgba(0,0,0,0.5)">
      <div class="modal-dialog">
        <div class="modal-content">
          <div class="modal-header">
            <h5 class="modal-title">Edit JWT Policy</h5>
            <button type="button" class="btn-close" @click="closeJWTPolicyModal"></button>
          </div>
          <div class="modal-body">
            <div class="mb-3">
              <label class="form-label" for="userJwtTtlDays">User JWT TTL (days)</label>
              <input
                id="userJwtTtlDays"
                v-model.number="jwtPolicyForm.userJwtTtlDays"
                type="number"
                min="0"
                class="form-control"
                placeholder="0 = never expires"
              />
              <div class="form-text">0 means user JWTs never expire.</div>
            </div>
            <div class="mb-3">
              <label class="form-label" for="accountJwtTtlDays">Account JWT TTL (days)</label>
              <input
                id="accountJwtTtlDays"
                v-model.number="jwtPolicyForm.accountJwtTtlDays"
                type="number"
                min="0"
                class="form-control"
                placeholder="0 = never expires"
              />
              <div class="form-text">0 means account JWTs never expire.</div>
            </div>
            <div class="mb-3">
              <label class="form-label" for="warnWindowDays">Warn Window (days)</label>
              <input
                id="warnWindowDays"
                v-model.number="jwtPolicyForm.warnWindowDays"
                type="number"
                min="0"
                class="form-control"
                placeholder="0 = off"
              />
              <div class="form-text">Emit expiry-warning events this many days before expiry. 0 = off.</div>
            </div>
            <div class="mb-3">
              <div class="form-check form-switch">
                <input
                  id="jwtAutoRenew"
                  v-model="jwtPolicyForm.jwtAutoRenew"
                  class="form-check-input"
                  type="checkbox"
                  role="switch"
                />
                <label class="form-check-label" for="jwtAutoRenew">Auto-Renew JWTs</label>
              </div>
              <div class="form-text">Automatically renew JWTs before they expire during the sweep.</div>
            </div>
            <div v-if="jwtPolicyError" class="alert alert-danger">{{ jwtPolicyError }}</div>
          </div>
          <div class="modal-footer">
            <button type="button" class="btn btn-secondary" @click="closeJWTPolicyModal">Cancel</button>
            <button type="button" class="btn btn-primary" :disabled="savingJWTPolicy" @click="saveJWTPolicy">
              <span v-if="savingJWTPolicy" class="spinner-border spinner-border-sm me-2"></span>
              Save
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- Backup Configure Modal -->
    <div v-if="showBackupConfigModal" class="modal fade show d-block" tabindex="-1" style="background-color: rgba(0,0,0,0.5)">
      <div class="modal-dialog">
        <div class="modal-content">
          <div class="modal-header">
            <h5 class="modal-title">Configure Backups</h5>
            <button type="button" class="btn-close" @click="closeBackupConfigModal"></button>
          </div>
          <div class="modal-body">
            <div class="mb-3">
              <div class="form-check form-switch">
                <input
                  id="backupEnabled"
                  v-model="backupConfigForm.enabled"
                  class="form-check-input"
                  type="checkbox"
                  role="switch"
                />
                <label class="form-check-label" for="backupEnabled">Enabled</label>
              </div>
            </div>
            <div class="mb-3">
              <label class="form-label" for="backupInterval">Interval</label>
              <select id="backupInterval" v-model.number="backupConfigForm.intervalSeconds" class="form-select">
                <option :value="3600">1h</option>
                <option :value="21600">6h</option>
                <option :value="43200">12h</option>
                <option :value="86400">24h</option>
                <option :value="172800">48h</option>
                <option :value="604800">7d</option>
              </select>
            </div>
            <div class="mb-3">
              <label class="form-label" for="backupRetention">Retention</label>
              <input
                id="backupRetention"
                v-model.number="backupConfigForm.retentionCount"
                type="number"
                min="0"
                class="form-control"
                placeholder="0 = keep all"
              />
              <div class="form-text">Keep at most N most recent backups (0 = keep all).</div>
            </div>
            <div v-if="backupConfigError" class="alert alert-danger">{{ backupConfigError }}</div>
          </div>
          <div class="modal-footer">
            <button type="button" class="btn btn-secondary" @click="closeBackupConfigModal">Cancel</button>
            <button type="button" class="btn btn-primary" :disabled="savingBackupConfig" @click="saveBackupConfig">
              <span v-if="savingBackupConfig" class="spinner-border spinner-border-sm me-2"></span>
              Save
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- Add Recipient Modal -->
    <div v-if="showAddRecipientModal" class="modal fade show d-block" tabindex="-1" style="background-color: rgba(0,0,0,0.5)">
      <div class="modal-dialog">
        <div class="modal-content">
          <div class="modal-header">
            <h5 class="modal-title">Add backup recipient</h5>
            <button type="button" class="btn-close" @click="closeAddRecipientModal"></button>
          </div>
          <div class="modal-body">
            <p class="text-muted small">
              Paste an age public key (<code>age1...</code> or <code>ssh-ed25519 ...</code>).
              Generate keypairs locally with <code>nisctl backup keygen</code> &mdash; NIS
              never sees the private half.
            </p>
            <div class="mb-3">
              <label class="form-label" for="recipientPubKey">Public key</label>
              <textarea
                id="recipientPubKey"
                v-model="addRecipientForm.publicKey"
                class="form-control font-monospace"
                rows="2"
                placeholder="age1..."
              ></textarea>
            </div>
            <div class="mb-3">
              <label class="form-label" for="recipientLabel">Label (optional)</label>
              <input
                id="recipientLabel"
                v-model="addRecipientForm.label"
                type="text"
                class="form-control"
                placeholder="ops-team"
              />
              <div class="form-text">Free-form description, e.g. owner or location.</div>
            </div>
            <div v-if="addRecipientError" class="alert alert-danger">{{ addRecipientError }}</div>
          </div>
          <div class="modal-footer">
            <button type="button" class="btn btn-secondary" @click="closeAddRecipientModal">Cancel</button>
            <button
              type="button"
              class="btn btn-primary"
              :disabled="addingRecipient || !addRecipientForm.publicKey.trim()"
              @click="submitAddRecipient"
            >
              <span v-if="addingRecipient" class="spinner-border spinner-border-sm me-2"></span>
              Add
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- Backup Modal -->
    <div v-if="showExportModal" class="modal fade show d-block" tabindex="-1" style="background-color: rgba(0,0,0,0.5)">
      <div class="modal-dialog">
        <div class="modal-content">
          <div class="modal-header">
            <h5 class="modal-title">Back up Operator</h5>
            <button type="button" class="btn-close" @click="closeExportModal"></button>
          </div>
          <div class="modal-body">
            <div class="mb-3">
              <label class="form-label">Format</label>
              <div class="form-check">
                <input
                  id="formatYaml"
                  v-model="exportFormat"
                  class="form-check-input"
                  type="radio"
                  value="yaml"
                />
                <label class="form-check-label" for="formatYaml">YAML</label>
              </div>
              <div class="form-check">
                <input
                  id="formatJson"
                  v-model="exportFormat"
                  class="form-check-input"
                  type="radio"
                  value="json"
                />
                <label class="form-check-label" for="formatJson">JSON</label>
              </div>
            </div>
            <div class="mb-3">
              <div class="form-check">
                <input
                  id="plaintextSecrets"
                  v-model="exportPlaintextSecrets"
                  class="form-check-input"
                  type="checkbox"
                />
                <label class="form-check-label" for="plaintextSecrets">
                  <strong class="text-danger">DANGER:</strong> back up seeds in plaintext
                </label>
                <div class="form-text text-danger">
                  Decrypts every NKey seed with the server's current encryption key and writes them
                  as plaintext in the file. Use only for disaster-recovery backups that must remain
                  readable if the encryption key is lost. The resulting file is equivalent to a
                  plaintext NKey vault — anyone with read access can mint credentials for every
                  entity in the backup. When unchecked, seeds stay encrypted (same on-disk form as
                  the database) and the destination must use the same encryption key to restore.
                </div>
              </div>
            </div>
            <div v-if="exportError" class="alert alert-danger">{{ exportError }}</div>
          </div>
          <div class="modal-footer">
            <button type="button" class="btn btn-secondary" @click="closeExportModal">Cancel</button>
            <button type="button" class="btn btn-primary" @click="handleExport" :disabled="exporting">
              <span v-if="exporting" class="spinner-border spinner-border-sm me-2"></span>
              Back up
            </button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { useRoute } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import apiClient from '@/utils/api'
import { backupClient } from '@/utils/clients'
import { ConnectError } from '@connectrpc/connect'
import CodeBlock from '@/components/CodeBlock.vue'
import ClickablePubKey from '@/components/ClickablePubKey.vue'

const route = useRoute()
const authStore = useAuthStore()
const operator = ref(null)
const clusters = ref([])
const loading = ref(false)
const error = ref('')
const config = ref('')
const showExportModal = ref(false)
const exportPlaintextSecrets = ref(false)
const exportFormat = ref('yaml') // matches nisctl default
const exporting = ref(false)
const exportError = ref('')
const hasAdminAccount = ref(false)
const checkingAdminAccount = ref(false)
const creatingAdminAccount = ref(false)
const adminAccountError = ref('')
let refreshInterval = null

// JWT Policy modal state
const showJWTPolicyModal = ref(false)
const jwtPolicyForm = ref({ userJwtTtlDays: 0, accountJwtTtlDays: 0, warnWindowDays: 0, jwtAutoRenew: false })
const savingJWTPolicy = ref(false)
const jwtPolicyError = ref('')

// Sweep state
const sweepRunning = ref(false)
const sweepResult = ref(null)
const sweepError = ref('')

// Backup state
const backupSettings = ref(null)
const backups = ref([])
const loadingBackups = ref(false)
const runningBackup = ref(false)
const backupError = ref('')
const backupsGloballyDisabled = ref(false)
const downloadingBackupId = ref(null)

// Backup Configure modal state
const showBackupConfigModal = ref(false)
const backupConfigForm = ref({ enabled: false, intervalSeconds: 86400, retentionCount: 0 })
const savingBackupConfig = ref(false)
const backupConfigError = ref('')

// Backup recipients (age) state
const recipients = ref([])
const loadingRecipients = ref(false)
const recipientsError = ref('')
const removingRecipientId = ref(null)
const lastActiveRecipientRemoved = ref(false)
const showAddRecipientModal = ref(false)
const addRecipientForm = ref({ publicKey: '', label: '' })
const addingRecipient = ref(false)
const addRecipientError = ref('')

const loadOperator = async () => {
  loading.value = true
  error.value = ''
  try {
    const response = await apiClient.post('/nis.v1.OperatorService/GetOperator', {
      id: route.params.id
    })
    operator.value = response.data.operator
    // Auto-generate config after loading operator
    await generateConfig()
    // Load clusters for this operator
    await loadClusters()
    // Check for admin account
    await checkAdminAccount()
    // Load backup settings, list, and recipients (non-fatal if not available)
    if (authStore.isAdmin || authStore.isOperatorAdmin) {
      await Promise.all([loadBackupSettings(), loadBackups(), loadRecipients()])
    }
  } catch (err) {
    error.value = err.response?.data?.message || 'Failed to load operator'
  } finally {
    loading.value = false
  }
}

const checkAdminAccount = async () => {
  checkingAdminAccount.value = true
  try {
    // Find system account by matching public key
    if (!operator.value.systemAccountPubKey) {
      hasAdminAccount.value = false
      return
    }

    const accountsResponse = await apiClient.post('/nis.v1.AccountService/ListAccounts', {
      operatorId: operator.value.id
    })
    const accounts = accountsResponse.data.accounts || []
    const sysAccount = accounts.find(account => account.publicKey === operator.value.systemAccountPubKey)

    if (!sysAccount) {
      hasAdminAccount.value = false
      return
    }

    // Then check if system user exists in system account
    const usersResponse = await apiClient.post('/nis.v1.UserService/ListUsers', {
      accountId: sysAccount.id
    })
    const users = usersResponse.data.users || []
    hasAdminAccount.value = users.some(user => user.name === 'system')
  } catch (err) {
    console.error('Failed to check system user:', err)
    hasAdminAccount.value = false
  } finally {
    checkingAdminAccount.value = false
  }
}

const createAdminAccount = async () => {
  creatingAdminAccount.value = true
  adminAccountError.value = ''
  try {
    // Find system account by matching public key
    if (!operator.value.systemAccountPubKey) {
      adminAccountError.value = 'No system account configured for this operator'
      return
    }

    const accountsResponse = await apiClient.post('/nis.v1.AccountService/ListAccounts', {
      operatorId: operator.value.id
    })
    const accounts = accountsResponse.data.accounts || []
    const sysAccount = accounts.find(account => account.publicKey === operator.value.systemAccountPubKey)

    if (!sysAccount) {
      adminAccountError.value = 'System account not found'
      return
    }

    // Check if system user already exists
    const usersResponse = await apiClient.post('/nis.v1.UserService/ListUsers', {
      accountId: sysAccount.id
    })
    const users = usersResponse.data.users || []
    let systemUser = users.find(user => user.name === 'system')

    // Create system user if it doesn't exist
    if (!systemUser) {
      const userResponse = await apiClient.post('/nis.v1.UserService/CreateUser', {
        accountId: sysAccount.id,
        name: 'system',
        description: 'System user for operator management'
      })
      systemUser = userResponse.data.user
    }

    // Update all clusters to use this system user for credentials
    if (clusters.value && clusters.value.length > 0) {
      for (const cluster of clusters.value) {
        try {
          await apiClient.post('/nis.v1.ClusterService/UpdateClusterCredentials', {
            id: cluster.id,
            systemAccountCreds: systemUser.id
          })
        } catch (clusterErr) {
          console.error(`Failed to update credentials for cluster ${cluster.name}:`, clusterErr)
        }
      }
    }

    hasAdminAccount.value = true
    // Reload clusters to show updated health status
    await loadClusters()
  } catch (err) {
    adminAccountError.value = err.response?.data?.message || 'Failed to create system user'
  } finally {
    creatingAdminAccount.value = false
  }
}

const loadClusters = async () => {
  try {
    const response = await apiClient.post('/nis.v1.ClusterService/ListClusters', {
      operatorId: operator.value.id
    })
    clusters.value = response.data.clusters || []
  } catch (err) {
    console.error('Failed to load clusters:', err)
  }
}

const generateConfig = async () => {
  try {
    const response = await apiClient.post('/nis.v1.OperatorService/GenerateInclude', {
      id: operator.value.id
    })
    config.value = response.data.config
  } catch (err) {
    error.value = err.response?.data?.message || 'Failed to generate config'
  }
}

const downloadConfig = () => {
  if (!config.value) return

  const blob = new Blob([config.value], { type: 'text/plain' })
  const url = window.URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `${operator.value.name}-nats-server.conf`
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  window.URL.revokeObjectURL(url)
}

const closeExportModal = () => {
  showExportModal.value = false
  exportError.value = ''
  exportPlaintextSecrets.value = false
  exportFormat.value = 'yaml'
}

const handleExport = async () => {
  exporting.value = true
  exportError.value = ''
  try {
    const response = await apiClient.post('/nis.v1.ExportService/ExportOperator', {
      operatorId: operator.value.id,
      plaintextSecrets: exportPlaintextSecrets.value,
      format: exportFormat.value
    }, {
      responseType: 'json'
    })

    // The server echoes back the format it actually produced; trust that for
    // the MIME type and file extension instead of guessing from the request.
    const actualFormat = response.data.format || exportFormat.value
    const mime = actualFormat === 'yaml' ? 'application/yaml' : 'application/json'
    const ext = actualFormat === 'yaml' ? 'yaml' : 'json'

    // Convert the base64 data to a blob and download.
    const fileContent = atob(response.data.data)
    const blob = new Blob([fileContent], { type: mime })
    const url = window.URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `${operator.value.name}-backup.${ext}`
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    window.URL.revokeObjectURL(url)

    closeExportModal()
  } catch (err) {
    exportError.value = err.response?.data?.message || 'Failed to back up operator'
  } finally {
    exporting.value = false
  }
}

const formatDate = (dateStr) => {
  if (!dateStr) return '-'
  return new Date(dateStr).toLocaleString()
}

// secondsVal may be a BigInt (from protobuf int64), a string, or a number.
const formatTTLDays = (secondsVal) => {
  const secs = Number(secondsVal)
  if (!secs || secs === 0) return 'Never'
  const days = Math.round(secs / 86400)
  return days === 1 ? '1 day' : `${days} days`
}

const openJWTPolicyModal = () => {
  const op = operator.value
  jwtPolicyForm.value = {
    userJwtTtlDays: op.userJwtTtlSeconds ? Math.round(Number(op.userJwtTtlSeconds) / 86400) : 0,
    accountJwtTtlDays: op.accountJwtTtlSeconds ? Math.round(Number(op.accountJwtTtlSeconds) / 86400) : 0,
    warnWindowDays: op.jwtWarnWindowSeconds ? Math.round(Number(op.jwtWarnWindowSeconds) / 86400) : 0,
    jwtAutoRenew: op.jwtAutoRenew || false,
  }
  jwtPolicyError.value = ''
  showJWTPolicyModal.value = true
}

const closeJWTPolicyModal = () => {
  showJWTPolicyModal.value = false
  jwtPolicyError.value = ''
}

const saveJWTPolicy = async () => {
  savingJWTPolicy.value = true
  jwtPolicyError.value = ''
  try {
    const f = jwtPolicyForm.value
    const resp = await apiClient.post('/nis.v1.OperatorService/SetJWTPolicy', {
      id: operator.value.id,
      userJwtTtlSeconds: String(f.userJwtTtlDays * 86400),
      accountJwtTtlSeconds: String(f.accountJwtTtlDays * 86400),
      jwtWarnWindowSeconds: String(f.warnWindowDays * 86400),
      jwtAutoRenew: f.jwtAutoRenew,
    })
    operator.value = resp.data.operator
    closeJWTPolicyModal()
  } catch (err) {
    jwtPolicyError.value = err.response?.data?.message || 'Failed to save JWT policy'
  } finally {
    savingJWTPolicy.value = false
  }
}

const runSweep = async () => {
  sweepRunning.value = true
  sweepResult.value = null
  sweepError.value = ''
  try {
    const resp = await apiClient.post('/nis.v1.OperatorService/RunJWTExpirySweep', {})
    sweepResult.value = resp.data
  } catch (err) {
    sweepError.value = err.response?.data?.message || 'Sweep failed'
  } finally {
    sweepRunning.value = false
  }
}

// Backup helpers
// Only the NIS-wide kill switch should trigger the "globally disabled" card.
// Other FailedPrecondition causes (e.g. no age recipients configured) must
// surface their own message via backupError, not be swallowed by this banner.
const isBackupsGloballyDisabled = (err) => {
  const msg = err?.message || ''
  return msg.includes('backups are disabled at the NIS-wide level')
}

const formatIntervalSeconds = (secs) => {
  const n = Number(secs)
  if (!n) return '-'
  if (n < 3600) return `${Math.round(n / 60)}m`
  if (n < 86400) return `${Math.round(n / 3600)}h`
  if (n < 604800) return `${Math.round(n / 86400)}d`
  return `${Math.round(n / 604800)}w`
}

const formatBytes = (bytesVal) => {
  const n = Number(bytesVal)
  if (!n) return '0 B'
  if (n < 1024) return `${n} B`
  if (n < 1048576) return `${(n / 1024).toFixed(1)} KB`
  if (n < 1073741824) return `${(n / 1048576).toFixed(1)} MB`
  return `${(n / 1073741824).toFixed(2)} GB`
}

const loadBackupSettings = async () => {
  if (!operator.value) return
  try {
    const resp = await backupClient.getOperatorBackupSettings({ operatorId: operator.value.id })
    backupSettings.value = resp.settings || null
    backupsGloballyDisabled.value = false
  } catch (err) {
    if (isBackupsGloballyDisabled(err)) {
      backupsGloballyDisabled.value = true
    }
    // non-fatal — settings panel still renders
  }
}

const loadBackups = async () => {
  if (!operator.value) return
  loadingBackups.value = true
  backupError.value = ''
  try {
    const resp = await backupClient.listOperatorBackups({ operatorId: operator.value.id })
    backups.value = resp.backups || []
    backupsGloballyDisabled.value = false
  } catch (err) {
    if (isBackupsGloballyDisabled(err)) {
      backupsGloballyDisabled.value = true
    } else {
      backupError.value = (err instanceof ConnectError ? err.message : err?.message) || 'Failed to load backups'
    }
  } finally {
    loadingBackups.value = false
  }
}

const runBackupNow = async () => {
  runningBackup.value = true
  backupError.value = ''
  try {
    await backupClient.runOperatorBackup({ operatorId: operator.value.id })
    await Promise.all([loadBackupSettings(), loadBackups()])
  } catch (err) {
    if (isBackupsGloballyDisabled(err)) {
      backupsGloballyDisabled.value = true
    } else {
      backupError.value = (err instanceof ConnectError ? err.message : err?.message) || 'Failed to run backup'
    }
  } finally {
    runningBackup.value = false
  }
}

const downloadBackup = async (bk) => {
  downloadingBackupId.value = bk.id
  backupError.value = ''
  try {
    const chunks = []
    for await (const msg of backupClient.downloadBackup({ id: bk.id })) {
      if (msg.payload.case === 'chunk') {
        chunks.push(msg.payload.value)
      }
    }
    const totalLen = chunks.reduce((s, c) => s + c.length, 0)
    const merged = new Uint8Array(totalLen)
    let offset = 0
    for (const c of chunks) {
      merged.set(c, offset)
      offset += c.length
    }
    const blob = new Blob([merged], { type: 'application/yaml' })
    const url = window.URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `${bk.id}.yaml`
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    window.URL.revokeObjectURL(url)
  } catch (err) {
    backupError.value = (err instanceof ConnectError ? err.message : err?.message) || 'Download failed'
  } finally {
    downloadingBackupId.value = null
  }
}

const deleteBackup = async (bk) => {
  if (!window.confirm(`Delete backup ${bk.id.slice(0, 8)}...? This cannot be undone.`)) return
  backupError.value = ''
  try {
    await backupClient.deleteBackup({ id: bk.id })
    await loadBackups()
  } catch (err) {
    backupError.value = (err instanceof ConnectError ? err.message : err?.message) || 'Failed to delete backup'
  }
}

const openBackupConfigModal = () => {
  const s = backupSettings.value
  backupConfigForm.value = {
    enabled: s ? s.enabled : false,
    intervalSeconds: s && Number(s.intervalSeconds) ? Number(s.intervalSeconds) : 86400,
    retentionCount: s ? s.retentionCount : 0,
  }
  backupConfigError.value = ''
  showBackupConfigModal.value = true
}

const closeBackupConfigModal = () => {
  showBackupConfigModal.value = false
  backupConfigError.value = ''
}

const saveBackupConfig = async () => {
  savingBackupConfig.value = true
  backupConfigError.value = ''
  try {
    const f = backupConfigForm.value
    const resp = await backupClient.updateOperatorBackupSettings({
      operatorId: operator.value.id,
      enabled: f.enabled,
      intervalSeconds: BigInt(f.intervalSeconds),
      retentionCount: f.retentionCount,
    })
    backupSettings.value = resp.settings || null
    await loadBackups()
    closeBackupConfigModal()
  } catch (err) {
    if (isBackupsGloballyDisabled(err)) {
      backupsGloballyDisabled.value = true
      closeBackupConfigModal()
    } else {
      backupConfigError.value = (err instanceof ConnectError ? err.message : err?.message) || 'Failed to save backup settings'
    }
  } finally {
    savingBackupConfig.value = false
  }
}

const loadRecipients = async () => {
  if (!operator.value) return
  loadingRecipients.value = true
  recipientsError.value = ''
  try {
    const resp = await backupClient.listBackupRecipients({ operatorId: operator.value.id })
    recipients.value = resp.recipients || []
    backupsGloballyDisabled.value = false
  } catch (err) {
    if (isBackupsGloballyDisabled(err)) {
      backupsGloballyDisabled.value = true
    } else {
      recipientsError.value = (err instanceof ConnectError ? err.message : err?.message) || 'Failed to load recipients'
    }
  } finally {
    loadingRecipients.value = false
  }
}

const openAddRecipientModal = () => {
  addRecipientForm.value = { publicKey: '', label: '' }
  addRecipientError.value = ''
  lastActiveRecipientRemoved.value = false
  showAddRecipientModal.value = true
}

const closeAddRecipientModal = () => {
  showAddRecipientModal.value = false
  addRecipientError.value = ''
}

const submitAddRecipient = async () => {
  addingRecipient.value = true
  addRecipientError.value = ''
  try {
    await backupClient.addBackupRecipient({
      operatorId: operator.value.id,
      publicKey: addRecipientForm.value.publicKey.trim(),
      label: addRecipientForm.value.label.trim(),
    })
    await loadRecipients()
    closeAddRecipientModal()
  } catch (err) {
    if (isBackupsGloballyDisabled(err)) {
      backupsGloballyDisabled.value = true
      closeAddRecipientModal()
    } else {
      addRecipientError.value = (err instanceof ConnectError ? err.message : err?.message) || 'Failed to add recipient'
    }
  } finally {
    addingRecipient.value = false
  }
}

const removeRecipient = async (r) => {
  const label = r.label || (r.publicKey ? r.publicKey.slice(0, 20) + '...' : r.id.slice(0, 8))
  if (!window.confirm(
    `Remove recipient "${label}"? Backups already encrypted to this key remain readable only ` +
    `by someone who still holds the matching private key; this action does not re-encrypt anything.`
  )) return
  removingRecipientId.value = r.id
  recipientsError.value = ''
  lastActiveRecipientRemoved.value = false
  try {
    const resp = await backupClient.removeBackupRecipient({
      operatorId: operator.value.id,
      recipientId: r.id,
    })
    if (resp.isLastActive) {
      lastActiveRecipientRemoved.value = true
    }
    await loadRecipients()
  } catch (err) {
    if (isBackupsGloballyDisabled(err)) {
      backupsGloballyDisabled.value = true
    } else {
      recipientsError.value = (err instanceof ConnectError ? err.message : err?.message) || 'Failed to remove recipient'
    }
  } finally {
    removingRecipientId.value = null
  }
}

const refreshData = async () => {
  // Refresh clusters and admin account status without showing loading spinner
  try {
    await Promise.all([
      loadClusters(),
      checkAdminAccount()
    ])
  } catch (err) {
    console.error('Failed to refresh data:', err)
  }
}

onMounted(() => {
  loadOperator()
  // Refresh every 5 seconds
  refreshInterval = setInterval(refreshData, 5000)
})

onBeforeUnmount(() => {
  if (refreshInterval) {
    clearInterval(refreshInterval)
  }
})
</script>

<style scoped>
dt {
  font-weight: 600;
}

dd {
  margin-bottom: 0.5rem;
}
</style>
