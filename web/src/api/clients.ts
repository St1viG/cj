import { createClient, type Transport } from "@connectrpc/connect";
import { AccountService } from "@gen/kondukter/api/v1/account_pb";
import { AdminService } from "@gen/kondukter/api/v1/admin_pb";
import { AuthService } from "@gen/kondukter/api/v1/auth_pb";
import { CatalogService } from "@gen/kondukter/api/v1/catalog_pb";
import { InstanceService } from "@gen/kondukter/api/v1/instance_pb";
import { PartnerService } from "@gen/kondukter/api/v1/partner_pb";
import { UsageService } from "@gen/kondukter/api/v1/usage_pb";

/** One typed client per public service (EnrollmentService is for agents). */
export function createClients(transport: Transport) {
  return {
    auth: createClient(AuthService, transport),
    catalog: createClient(CatalogService, transport),
    instances: createClient(InstanceService, transport),
    account: createClient(AccountService, transport),
    usage: createClient(UsageService, transport),
    partner: createClient(PartnerService, transport),
    admin: createClient(AdminService, transport),
  };
}

export type Clients = ReturnType<typeof createClients>;
