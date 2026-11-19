package audit

import "context"

type MembershipEvent struct {
	Tenant, Actor, Member, Action, Reason string
	PreviousActive, Active                bool
	Revision, RevokedSessions             int64
	RevokedRoles                          []string
}

func RecordMembership(ctx context.Context, tx Writer, event MembershipEvent) error {
	_, err := tx.Exec(ctx, `INSERT INTO hive.membership_lifecycle_audit
		(tenant_id,actor_id,member_id,action,reason_code,previous_active,active,revision,revoked_sessions,revoked_roles)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb)`,
		event.Tenant, event.Actor, event.Member, event.Action, event.Reason, event.PreviousActive, event.Active,
		event.Revision, event.RevokedSessions, event.RevokedRoles)
	return err
}
