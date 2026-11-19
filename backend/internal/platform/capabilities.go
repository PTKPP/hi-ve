// Package platform owns the shared registry of canonical capabilities.
package platform

import "slices"

// pilotCapabilities follows the P0 submodule registry in PRD §07.7.
// platform.administration is internal to Platform, regardless of tenant bundles.
var pilotCapabilities = [...]string{
	"platform.tenancy",
	"platform.entitlements",
	"platform.administration",
	"identity.authentication",
	"identity.users",
	"identity.permissions",
	"identity.offline_access",
	"devices.management",
	"devices.operators",
	"settings.tenant",
	"audit.activity",
	"catalog.products",
	"catalog.units",
	"catalog.pricing",
	"locations.management",
	"inventory.balance",
	"inventory.ledger",
	"inventory.receiving",
	"inventory.repacking",
	"inventory.transfer",
	"inventory.adjustment",
	"inventory.stock_count",
	"inventory.traceability",
	"sales.cashier",
	"sales.transactions",
	"sales.payments",
	"sales.history",
	"sales.returns",
	"sales.voids",
	"sales.receipts",
	"reports.dashboard",
	"reports.sales",
	"reports.inventory",
	"reports.stock_movements",
	"offline.storage",
	"offline.sync",
	"offline.reconciliation",
	"realtime.events",
	"realtime.notifications",
	"realtime.connection_status",
}

// PilotCapabilities returns a caller-owned list of canonical P0 submodule IDs.
func PilotCapabilities() []string {
	return slices.Clone(pilotCapabilities[:])
}

// IsPilotCapability reports exact membership in the P0 registry.
// Entitlement, permission, and rollout decisions are evaluated separately.
func IsPilotCapability(id string) bool {
	return slices.Contains(pilotCapabilities[:], id)
}
