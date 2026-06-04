import { createPromiseClient, type PromiseClient } from '@connectrpc/connect'
import { createConnectTransport } from '@connectrpc/connect-web'
import { useAuthStore } from '@/stores/auth'

import { OperatorService } from '@/gen/nis/v1/operator_connect'
import { AccountService } from '@/gen/nis/v1/account_connect'
import { UserService } from '@/gen/nis/v1/user_connect'
import { ClusterService } from '@/gen/nis/v1/cluster_connect'
import { ScopedSigningKeyService } from '@/gen/nis/v1/scoped_key_connect'
import { AuthService } from '@/gen/nis/v1/auth_connect'
import { EventService } from '@/gen/nis/v1/event_connect'
import { WebhookService } from '@/gen/nis/v1/webhook_connect'
import { APITokenService } from '@/gen/nis/v1/api_token_connect'
import { TemplateService } from '@/gen/nis/v1/template_connect'
import { JobService } from '@/gen/nis/v1/job_connect'
import { BackupService } from '@/gen/nis/v1/backup_connect'
import { ConfigService } from '@/gen/nis/v1/config_connect'
import { OrganizationService } from '@/gen/nis/v1/organization_connect'

// Determine API base URL
const API_BASE_URL = import.meta.env.DEV
  ? 'http://localhost:8081'
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

// Create typed clients
const transport = createAuthTransport()

export const operatorClient: PromiseClient<typeof OperatorService> = createPromiseClient(OperatorService, transport)
export const accountClient: PromiseClient<typeof AccountService> = createPromiseClient(AccountService, transport)
export const userClient: PromiseClient<typeof UserService> = createPromiseClient(UserService, transport)
export const clusterClient: PromiseClient<typeof ClusterService> = createPromiseClient(ClusterService, transport)
export const scopedKeyClient: PromiseClient<typeof ScopedSigningKeyService> = createPromiseClient(ScopedSigningKeyService, transport)
export const authClient: PromiseClient<typeof AuthService> = createPromiseClient(AuthService, transport)
export const eventClient: PromiseClient<typeof EventService> = createPromiseClient(EventService, transport)
export const webhookClient: PromiseClient<typeof WebhookService> = createPromiseClient(WebhookService, transport)
export const apiTokenClient: PromiseClient<typeof APITokenService> = createPromiseClient(APITokenService, transport)
export const templateClient: PromiseClient<typeof TemplateService> = createPromiseClient(TemplateService, transport)
export const jobClient: PromiseClient<typeof JobService> = createPromiseClient(JobService, transport)
export const backupClient: PromiseClient<typeof BackupService> = createPromiseClient(BackupService, transport)
export const configClient: PromiseClient<typeof ConfigService> = createPromiseClient(ConfigService, transport)
export const organizationClient: PromiseClient<typeof OrganizationService> = createPromiseClient(OrganizationService, transport)
