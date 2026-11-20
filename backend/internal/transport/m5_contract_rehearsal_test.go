package transport

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"hi-ve/backend/internal/audit"
	"hi-ve/backend/internal/identity"
	"hi-ve/backend/internal/platform/provisioning"
)

const m5LegacyFK = "tenant_memberships_principal_id_fkey"

func TestM5ContractRehearsal(t *testing.T) {
	legacyPath, rollbackPath, candidatePath := m5NativePaths(t)
	db := disposableDatabase(t)
	runtimeDSN := m5RuntimeDSN(t, db)
	verifyM5RuntimeRole(t, runtimeDSN)
	proxy, ca, sign := m5OIDCNetwork(t)
	legacy, stopLegacy := startM5Process(t, legacyPath, runtimeDSN, proxy, ca)
	rollback, stopRollback := startM5Process(t, rollbackPath, runtimeDSN, proxy, ca)
	candidate, stopCandidate := startM5Process(t, candidatePath, runtimeDSN, proxy, ca)

	m5Exec(t, db, `WITH principal AS (
		INSERT INTO hive.google_principals (issuer,subject) VALUES ('https://accounts.google.com','m5-platform-admin') RETURNING id
	), admin AS (
		INSERT INTO hive.platform_principals (principal_id) SELECT id FROM principal RETURNING principal_id
	) INSERT INTO hive.platform_permissions SELECT principal_id, permission FROM admin
		CROSS JOIN (VALUES ('platform.tenants.provision'),('platform.tenants.activate')) p(permission)`)
	admin := m5Login(t, legacy, candidate, sign, "/api/v1/admin/login", adminOrigin, "m5-platform-admin", nil, 204).
		cookie(t, platformCookie)

	catalogBefore := m5Catalog(t, db)
	m5ExpectSchema(t, db, 28, catalogBefore)
	before := m5ExercisePair(t, db, legacy, candidate, sign, admin, "before-contract")
	savedOwner := m5Login(t, candidate, rollback, sign, before.path+"/login", appOrigin, before.subject, nil, 204).cookie(t, tenantCookie)
	savedHistory := m5History(t, candidate, before.path, savedOwner)
	savedRows := m5Snapshot(t, db, before.tenant)
	if len(savedHistory.Events) == 0 {
		t.Fatal("missing baseline history before Contract")
	}
	ownerLink := m5Scalar(t, db, `SELECT owner_google_principal_id::text FROM hive.tenant_memberships
		WHERE tenant_id=$1 AND principal_id=$2`, before.tenant, before.principal)
	if ownerLink != before.principal {
		t.Fatal("Owner-specific Google anchor is missing")
	}

	runner := m5MigrationDir(t)
	candidateSQL := filepath.Join(m5BackendRoot(t), "scripts", "fixtures", "m5", "029_m5_contract_candidate.sql")
	m5CopyFile(t, candidateSQL, filepath.Join(runner, "029_m5_contract_candidate.sql"))
	t.Run("lock timeout retains original version and constraint", func(t *testing.T) {
		tx, err := db.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(t.Context())
		if _, err = tx.Exec(t.Context(), `LOCK TABLE hive.tenant_memberships IN ROW SHARE MODE`); err != nil {
			t.Fatal(err)
		}
		m5CheckMigrate(t, runner, "lock timeout")
		m5ExpectSchema(t, db, 28, catalogBefore)
		rollback.expect(t, 200, "GET", before.path+"/session", appOrigin, "", nil, savedOwner)
	})
	t.Run("failure after DDL rolls back constraint and version", func(t *testing.T) {
		faultDir := m5MigrationDir(t)
		body, err := os.ReadFile(candidateSQL)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(faultDir, "029_m5_injected_failure.sql"), append(body, []byte("\nSELECT 1/0;\n")...), 0600); err != nil {
			t.Fatal(err)
		}
		m5CheckMigrate(t, faultDir, "division by zero")
		m5ExpectSchema(t, db, 28, catalogBefore)
	})

	m5CheckMigrate(t, runner, "")
	catalogAfter := make(map[string]string, len(catalogBefore)-1)
	for name, def := range catalogBefore {
		if name != m5LegacyFK {
			catalogAfter[name] = def
		}
	}
	m5ExpectSchema(t, db, 29, catalogAfter)
	m5CheckMigrate(t, runner, "")
	m5ExpectSchema(t, db, 29, catalogAfter)
	t.Log("PASS: candidate and repeat migration changed only the legacy FK")

	// All processes remain up while the actual schema changes. The legacy
	// writer omits the new column, the current writer supplies it explicitly.
	adminAfter := m5Login(t, candidate, legacy, sign, "/api/v1/admin/login", adminOrigin, "m5-platform-admin", nil, 204).
		cookie(t, platformCookie)
	m5ExercisePair(t, db, legacy, candidate, sign, adminAfter, "after-contract")
	candidate.expect(t, 204, "POST", "/api/v1/admin/logout", adminOrigin, "", nil, adminAfter)
	m5VerifyPreserved(t, db, rollback, before, savedOwner, savedHistory, savedRows)
	t.Log("PASS: independent legacy and current HTTP writers and cross-binary activation race before and after Contract")

	// Drain the fixture processes before restoring the legacy constraint.
	stopLegacy()
	stopRollback()
	stopCandidate()

	// Restore the FK through a forward migration on the real fixture schema.
	const restore = `SET LOCAL lock_timeout = '5s';
ALTER TABLE hive.tenant_memberships ADD CONSTRAINT tenant_memberships_principal_id_fkey
    FOREIGN KEY (principal_id) REFERENCES hive.google_principals(id) NOT VALID;
ALTER TABLE hive.tenant_memberships VALIDATE CONSTRAINT tenant_memberships_principal_id_fkey;
`
	addSQL := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(runner, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if count := m5Scalar(t, db, `SELECT count(*)::text FROM hive.tenant_memberships m
		WHERE NOT EXISTS(SELECT 1 FROM hive.google_principals p WHERE p.id=m.principal_id)`); count != "0" {
		t.Fatalf("expected zero membership orphans before restoring FK, got %s", count)
	}
	addSQL("030_m5_restore_before_neutral_data.sql", restore)
	m5CheckMigrate(t, runner, "")
	m5ExpectSchema(t, db, 30, catalogBefore)
	restored, stopRestored := startM5Process(t, rollbackPath, runtimeDSN, proxy, ca)
	m5VerifyPreserved(t, db, restored, before, savedOwner, savedHistory, savedRows)
	stopRestored()
	t.Log("PASS: forward FK restoration before any neutral-only memberships")

	m5CopyFile(t, candidateSQL, filepath.Join(runner, "031_m5_contract_after_restore.sql"))
	m5CheckMigrate(t, runner, "")
	m5ExpectSchema(t, db, 31, catalogAfter)
	neutralID := m5Scalar(t, db, `WITH principal AS (
		INSERT INTO hive.identity_principals(id) VALUES(gen_random_uuid()) RETURNING id
	) INSERT INTO hive.tenant_memberships(tenant_id,principal_id,role)
		SELECT $1,id,'staff' FROM principal RETURNING principal_id::text`, before.tenant)
	m5TestRetainedConstraints(t, db, before)
	addSQL("032_m5_restore_with_neutral_data.sql", restore)
	m5CheckMigrate(t, runner, "foreign key constraint")
	m5ExpectSchema(t, db, 31, catalogAfter)
	current, stopCurrent := startM5Process(t, candidatePath, runtimeDSN, proxy, ca)
	m5VerifyPreserved(t, db, current, before, savedOwner, savedHistory, savedRows)
	m5Login(t, current, current, sign, before.path+"/login", appOrigin, "m5-neutral-no-link", nil, 403)
	stopCurrent()

	// Start a fresh copy of the selected rollback release on the same data.
	recovered, _ := startM5Process(t, rollbackPath, runtimeDSN, proxy, ca)
	m5VerifyPreserved(t, db, recovered, before, savedOwner, savedHistory, savedRows)
	m5Lifecycle(t, db, recovered, recovered, before, savedOwner, neutralID, "neutral-after-restore")
	m5Login(t, recovered, recovered, sign, before.path+"/login", appOrigin, "m5-neutral-no-link", nil, 403)
	fresh := m5Login(t, recovered, recovered, sign, before.path+"/login", appOrigin, before.subject, nil, 204).cookie(t, tenantCookie)
	recovered.expect(t, 204, "POST", before.path+"/logout", appOrigin, "", nil, fresh)
	recovered.expect(t, 401, "GET", before.path+"/session", appOrigin, "", nil, fresh)
	if m5Scalar(t, db, `SELECT count(*)::text FROM hive.tenant_memberships m JOIN hive.identity_principals p ON p.id=m.principal_id
		WHERE m.tenant_id=$1 AND m.principal_id=$2 AND m.active AND m.lifecycle_revision=2
		  AND NOT EXISTS(SELECT 1 FROM hive.google_principals g WHERE g.id=m.principal_id)`, before.tenant, neutralID) != "1" {
		t.Fatal("recovery did not retain the neutral member UUID and lifecycle")
	}
	t.Log("PASS: blocked FK restoration with neutral-only data, restarted rollback binary preserves the member and history")
}

type m5Tenant struct {
	path, tenant, principal, subject string
}

func m5ExercisePair(t *testing.T, db *pgxpool.Pool, legacy, candidate m5Process,
	sign func(string, string, string) string, admin *http.Cookie, phase string) m5Tenant {
	t.Helper()
	first := m5Provision(t, db, legacy, candidate, sign, admin, phase+"-legacy", false)
	second := m5Provision(t, db, candidate, legacy, sign, admin, phase+"-candidate", false)
	m5Provision(t, db, candidate, legacy, sign, admin, phase+"-race", true)
	for _, item := range []struct {
		tenant, foreign m5Tenant
		start, finish   m5Process
	}{
		{first, second, legacy, candidate}, {second, first, candidate, legacy},
	} {
		owner := m5Login(t, item.start, item.finish, sign, item.tenant.path+"/login", appOrigin, item.tenant.subject, nil, 204).cookie(t, tenantCookie)
		item.start.expect(t, 200, "GET", item.tenant.path+"/session", appOrigin, "", nil, owner)
		item.start.expect(t, 401, "GET", item.foreign.path+"/login-history", appOrigin, "", nil, owner)
		item.start.expect(t, 401, "GET", item.foreign.path+"/session", appOrigin, "", nil, owner)
		history := m5History(t, item.start, item.tenant.path, owner)
		if len(history.Events) != 1 || history.Events[0].Actor != item.tenant.principal || history.Events[0].Outcome != "success" {
			t.Fatal("expected one tenant-scoped Owner event from the other binary")
		}
		staffSubject := item.tenant.subject + "-staff"
		member := m5Scalar(t, db, `WITH p AS (INSERT INTO hive.google_principals(issuer,subject)
			VALUES ('https://accounts.google.com',$2) RETURNING id)
			INSERT INTO hive.tenant_memberships(tenant_id,principal_id,role) SELECT $1,id,'staff' FROM p RETURNING principal_id::text`, item.tenant.tenant, staffSubject)
		m5Exec(t, db, `WITH r AS (
			INSERT INTO hive.tenant_roles(tenant_id,role_key,name) VALUES($1,'fixture','Fixture') RETURNING id
		) INSERT INTO hive.tenant_role_assignments(tenant_id,principal_id,role_id) SELECT $1,$2,id FROM r`, item.tenant.tenant, member)
		item.start.expect(t, 401, "POST", item.foreign.path+"/members/"+member+"/deactivate", appOrigin,
			"m5-isolation-"+phase, map[string]any{"revision": 0, "reason_code": "administrative_action"}, owner)
		m5Lifecycle(t, db, item.start, item.finish, item.tenant, owner, member, phase)
		m5Login(t, item.finish, item.start, sign, item.tenant.path+"/login", appOrigin, staffSubject, nil, 403)
		item.finish.expect(t, 204, "POST", item.tenant.path+"/logout", appOrigin, "", nil, owner)
		item.start.expect(t, 401, "GET", item.tenant.path+"/session", appOrigin, "", nil, owner)
	}
	return first
}

func m5Provision(t *testing.T, db *pgxpool.Pool, writer, peer m5Process,
	sign func(string, string, string) string, admin *http.Cookie, label string, race bool) m5Tenant {
	t.Helper()
	createInput := provisioning.CreateInput{DisplayName: label, Email: label + "@example.test"}
	const create = "/api/v1/admin/tenants"
	createKey := "m5-create-" + label
	initial := writer.expect(t, 201, "POST", create, adminOrigin, createKey, createInput, admin)
	replayed := peer.expect(t, 201, "POST", create, adminOrigin, createKey, createInput, admin)
	tenant := initial.field(t, "tenant_id")
	if replayed.field(t, "tenant_id") != tenant {
		t.Fatal("create replay changed the tenant identity")
	}
	// Use the actual invitation row and OIDC claim path. SMTP delivery is
	// outside this compatibility probe, so the fixture delivers a known code.
	code := rand.Text()
	m5Exec(t, db, `UPDATE hive.owner_invitations SET digest=$2 WHERE tenant_id=$1`, tenant, identity.Digest(code))
	subject := "m5-owner-" + label
	claim := m5Login(t, writer, peer, sign, "/api/v1/app/owner-claim", appOrigin, subject,
		map[string]string{"invitation": code}, 200).field(t, "owner_claim_id")
	input := provisioning.ActivateInput{Claim: claim, Method: "verified-organization-contact", Reference: "m5-rehearsal"}
	path := create + "/" + tenant + "/activate"
	firstKey, secondKey := "m5-first-"+label, "m5-second-"+label
	replayKey := firstKey
	if race {
		type result struct {
			key  string
			code int
			err  error
		}
		start := make(chan struct{})
		results := make(chan result, 2)
		for _, attempt := range []struct {
			writer m5Process
			key    string
		}{{writer, firstKey}, {peer, secondKey}} {
			go func() {
				<-start
				response, err := attempt.writer.call("POST", path, adminOrigin, attempt.key, input, admin)
				results <- result{attempt.key, response.code, err}
			}()
		}
		close(start)
		a, b := <-results, <-results
		if a.code != 200 {
			a, b = b, a
		}
		if a.err != nil || b.err != nil || a.code != 200 || b.code != 409 {
			t.Fatalf("cross-binary activation race = %d/%d (%v/%v)", a.code, b.code, a.err, b.err)
		}
		replayKey = a.key
	} else {
		writer.expect(t, 200, "POST", path, adminOrigin, firstKey, input, admin)
	}
	peer.expect(t, 200, "POST", path, adminOrigin, replayKey, input, admin)
	if m5Scalar(t, db, `SELECT count(*)::text FROM hive.provisioning_audit WHERE tenant_id=$1 AND action='activate' AND outcome='succeeded'`, tenant) != "1" {
		t.Fatal("activation did not preserve exactly one success audit")
	}
	principal := m5Scalar(t, db, `SELECT m.principal_id::text FROM hive.tenant_memberships m JOIN hive.identity_principals p ON p.id=m.principal_id
		WHERE m.tenant_id=$1 AND m.role='primary_owner' AND m.active AND m.owner_google_principal_id=m.principal_id`, tenant)
	return m5Tenant{path: "/api/v1/app/tenants/" + tenant, tenant: tenant, principal: principal, subject: subject}
}

func m5Lifecycle(t *testing.T, db *pgxpool.Pool, a, b m5Process, tenant m5Tenant, owner *http.Cookie, member, key string) {
	t.Helper()
	path := tenant.path + "/members/" + member
	fixtureSession := &http.Cookie{Name: tenantCookie, Value: rand.Text()}
	m5Exec(t, db, `INSERT INTO hive.tenant_sessions(digest,tenant_id,principal_id,expires_at,auth_method)
		VALUES($1,$2,$3,clock_timestamp()+interval '10 minutes','device_pin')`,
		identity.Digest(fixtureSession.Value), tenant.tenant, member)
	b.expect(t, 200, "GET", tenant.path+"/session", appOrigin, "", nil, fixtureSession)
	change := map[string]any{"revision": 0, "reason_code": "administrative_action"}
	a.expect(t, 200, "POST", path+"/deactivate", appOrigin, "m5-disable-"+key, change, owner)
	b.expect(t, 200, "POST", path+"/deactivate", appOrigin, "m5-disable-"+key, change, owner)
	change["revision"] = 1
	b.expect(t, 200, "POST", path+"/reactivate", appOrigin, "m5-enable-"+key, change, owner)
	a.expect(t, 401, "GET", tenant.path+"/session", appOrigin, "", nil, fixtureSession)
	if m5Scalar(t, db, `SELECT count(*)::text FROM hive.membership_lifecycle_audit WHERE tenant_id=$1 AND member_id=$2`,
		tenant.tenant, member) != "2" {
		t.Fatal("lifecycle replay duplicated or lost history")
	}
	if m5Scalar(t, db, `SELECT count(*)::text FROM hive.tenant_role_assignments WHERE tenant_id=$1 AND principal_id=$2`,
		tenant.tenant, member) != "0" {
		t.Fatal("reactivation restored a previously revoked grant")
	}
}

func verifyM5RuntimeRole(t *testing.T, dsn string) {
	t.Helper()
	db, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if m5Scalar(t, db, `SELECT (current_user='hive_m5_runtime' AND NOT rolsuper AND NOT rolcreatedb AND NOT rolcreaterole
		AND NOT has_schema_privilege('hive','CREATE') AND NOT has_table_privilege('public.hive_schema_version','UPDATE')
		AND NOT pg_has_role((SELECT relowner FROM pg_class WHERE oid='hive.tenant_memberships'::regclass),'MEMBER'))::text
		FROM pg_roles WHERE rolname=current_user`) != "true" {
		t.Fatal("rehearsal must use the non-owner role without DDL privileges")
	}
}

func m5TestRetainedConstraints(t *testing.T, db *pgxpool.Pool, tenant m5Tenant) {
	t.Helper()
	check := func(name, sql string, args ...any) {
		t.Helper()
		_, err := db.Exec(t.Context(), sql, args...)
		var p *pgconn.PgError
		if !errors.As(err, &p) || p.ConstraintName != name {
			t.Fatalf("expected %s to reject the write: %v", name, err)
		}
	}
	check("membership_identity_principal_fk", `INSERT INTO hive.tenant_memberships(tenant_id,principal_id,role) VALUES($1,gen_random_uuid(),'staff')`, tenant.tenant)
	neutral := m5Scalar(t, db, `INSERT INTO hive.identity_principals(id) VALUES(gen_random_uuid()) RETURNING id::text`)
	candidate := m5Scalar(t, db, `INSERT INTO hive.tenants(display_name,status) VALUES('m5 constraint fixture','pending') RETURNING id::text`)
	ownerID := m5Scalar(t, db, `INSERT INTO hive.owner_candidates(tenant_id,contact_email) VALUES($1,'fixture@example.test') RETURNING id::text`, candidate)
	check("membership_owner_google_fk", `INSERT INTO hive.tenant_memberships(tenant_id,principal_id,role,owner_id)
		VALUES($1,$2,'primary_owner',$3)`, candidate, neutral, ownerID)
	check("membership_owner_google_fk", `DELETE FROM hive.google_principals WHERE id=$1`, tenant.principal)
	linked := m5Scalar(t, db, `INSERT INTO hive.google_principals(issuer,subject) VALUES('https://accounts.google.com','m5-duplicate-owner') RETURNING id::text`)
	check("tenant_active_primary_owner", `INSERT INTO hive.tenant_memberships(tenant_id,principal_id,role,owner_id)
		SELECT $1,$2,'primary_owner',owner_id FROM hive.tenant_memberships WHERE tenant_id=$1 AND principal_id=$3`, tenant.tenant, linked, tenant.principal)
}

func m5VerifyPreserved(t *testing.T, db *pgxpool.Pool, p m5Process, tenant m5Tenant, cookie *http.Cookie, history audit.LoginPage, savedRows string) {
	t.Helper()
	if p.expect(t, 200, "GET", tenant.path+"/session", appOrigin, "", nil, cookie).field(t, "principal_id") != tenant.principal {
		t.Fatal("Owner UUID changed during upgrade or rollback")
	}
	got := m5History(t, p, tenant.path, cookie)
	if len(got.Events) != len(history.Events) {
		t.Fatal("existing login history changed")
	}
	for i, event := range history.Events {
		if got.Events[i] != event {
			t.Fatal("existing login event identity or contents changed")
		}
	}
	if m5Snapshot(t, db, tenant.tenant) != savedRows {
		t.Fatal("existing membership UUIDs, Owner links or audit rows changed")
	}
}

// Exclude the subsequently added neutral-only fixture and its audit events.
func m5Snapshot(t *testing.T, db *pgxpool.Pool, tenant string) string {
	t.Helper()
	return m5Scalar(t, db, `SELECT jsonb_build_object(
		'memberships',(SELECT jsonb_agg(to_jsonb(m) ORDER BY m.principal_id)
			FROM hive.tenant_memberships m JOIN hive.google_principals g ON g.id=m.principal_id WHERE m.tenant_id=$1),
		'activation',(SELECT jsonb_agg(to_jsonb(a) ORDER BY a.id)
			FROM hive.provisioning_audit a WHERE a.tenant_id=$1),
		'lifecycle',(SELECT jsonb_agg(to_jsonb(a) ORDER BY a.id) FROM hive.membership_lifecycle_audit a
			JOIN hive.google_principals g ON g.id=a.member_id WHERE a.tenant_id=$1)
	)::text`, tenant)
}

func m5History(t *testing.T, p m5Process, path string, cookie *http.Cookie) audit.LoginPage {
	t.Helper()
	response := p.expect(t, 200, "GET", path+"/login-history", appOrigin, "", nil, cookie)
	var page audit.LoginPage
	if err := json.Unmarshal(response.data, &page); err != nil {
		t.Fatal(err)
	}
	return page
}

func m5Catalog(t *testing.T, db *pgxpool.Pool) map[string]string {
	t.Helper()
	rows, err := db.Query(t.Context(), `SELECT conname::text, pg_get_constraintdef(oid)||':'||convalidated::text
		FROM pg_constraint WHERE conrelid='hive.tenant_memberships'::regclass
		UNION ALL SELECT indexname,indexdef FROM pg_indexes WHERE schemaname='hive' AND tablename='tenant_memberships'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	catalog := map[string]string{}
	for rows.Next() {
		var name, def string
		if err = rows.Scan(&name, &def); err != nil {
			t.Fatal(err)
		}
		catalog[name] = def
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return catalog
}

func m5ExpectSchema(t *testing.T, db *pgxpool.Pool, version int, want map[string]string) {
	t.Helper()
	if actual := m5Scalar(t, db, `SELECT version::text FROM public.hive_schema_version`); actual != fmt.Sprint(version) {
		t.Fatalf("expected version %d, got %s", version, actual)
	}
	actual := m5Catalog(t, db)
	if len(actual) != len(want) {
		t.Fatalf("schema %d: got %d constraints/indexes, want %d", version, len(actual), len(want))
	}
	for name, def := range want {
		if actual[name] != def {
			t.Fatalf("schema %d: definition or validation changed for %s", version, name)
		}
	}
	for _, name := range []string{"membership_identity_principal_fk", "membership_owner_google_fk", "membership_owner_google_shape"} {
		if !strings.HasSuffix(actual[name], ":true") {
			t.Fatalf("required constraint %s is missing or unvalidated", name)
		}
	}
	if !strings.Contains(actual["tenant_active_primary_owner"], "UNIQUE") {
		t.Fatal("active Owner unique index is missing")
	}
}

func m5Exec(t *testing.T, db *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := db.Exec(t.Context(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func m5Scalar(t *testing.T, db *pgxpool.Pool, sql string, args ...any) string {
	t.Helper()
	var value string
	if err := db.QueryRow(t.Context(), sql, args...).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}
