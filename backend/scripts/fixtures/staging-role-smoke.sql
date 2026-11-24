BEGIN;
INSERT INTO hive.google_principals(issuer,subject) VALUES ('https://accounts.google.com','fixture-platform-admin');
INSERT INTO hive.platform_principals(principal_id)
SELECT id FROM hive.google_principals WHERE subject='fixture-platform-admin';
INSERT INTO hive.platform_permissions SELECT principal_id,'platform.tenants.read' FROM hive.platform_principals;
SET LOCAL ROLE hive_app;
DO $$
DECLARE
    org uuid;
    actor uuid;
    outlet uuid;
    pending text := repeat('0',48);
BEGIN
    INSERT INTO hive.tenants(display_name,status) VALUES ('Staging fixture','active') RETURNING id INTO org;
    INSERT INTO hive.google_principals(issuer,subject) VALUES ('https://accounts.google.com','fixture-actor') RETURNING id INTO actor;
    INSERT INTO hive.tenant_memberships(tenant_id,principal_id,role) VALUES (org,actor,'staff');
    PERFORM id FROM hive.active_tenants WHERE id=org FOR SHARE;
    INSERT INTO hive.locations(tenant_id,code,name,kind) VALUES (org,'smoke','Smoke','outlet') RETURNING id INTO outlet;
    IF NOT EXISTS (SELECT 1 FROM hive.inventory_locations WHERE tenant_id=org AND location_id=outlet) THEN
        RAISE EXCEPTION 'Inventory location projection did not run';
    END IF;

    INSERT INTO hive.device_pairings(id,public_key,thumbprint,start_key,code_key_id,nonce)
    VALUES(pending,'{}',sha256('fixture'::bytea),'key','key','nonce');
    UPDATE hive.device_pairings SET tenant_id=org,actor_id=actor,approval_key='fixture',approval_fingerprint=sha256('approved'::bytea)
    WHERE id=pending;
    UPDATE hive.tenants SET status='suspended' WHERE id=org;
    UPDATE hive.tenants SET status='active' WHERE id=org;
    BEGIN
        UPDATE hive.device_pairings SET redeem_key='fixture',redeem_proof=sha256('proof'::bytea) WHERE id=pending;
        RAISE EXCEPTION 'Stale pairing redeemed';
    EXCEPTION WHEN check_violation THEN NULL;
    END;

    INSERT INTO hive.sales_orders(tenant_id,id,outlet_id,actor_id,fingerprint,total,method,outlet_name)
    VALUES(org,gen_random_uuid(),outlet,actor,sha256('sale'::bytea),100,'manual_non_cash','Smoke');
    BEGIN
        CREATE TABLE hive.ddl_should_fail(id integer);
        RAISE EXCEPTION 'Runtime role has DDL privilege';
    EXCEPTION WHEN insufficient_privilege THEN NULL;
    END;
    BEGIN
        UPDATE hive.tenant_administration_audit SET reason='fixture';
        RAISE EXCEPTION 'Runtime role can rewrite audit';
    EXCEPTION WHEN insufficient_privilege THEN NULL;
    END;
    BEGIN
        UPDATE hive.platform_permissions SET permission='platform.tenants.provision';
        RAISE EXCEPTION 'Platform grant writable';
    EXCEPTION WHEN insufficient_privilege THEN NULL;
    END;
    BEGIN
        UPDATE hive.platform_principals SET active=false;
        RAISE EXCEPTION 'Platform activation writable';
    EXCEPTION WHEN insufficient_privilege THEN NULL;
    END;
    BEGIN
        UPDATE hive.google_principals SET subject='changed' WHERE subject='fixture-platform-admin';
        RAISE EXCEPTION 'Google principal mutable';
    EXCEPTION WHEN insufficient_privilege THEN NULL;
    END;
    BEGIN
        DELETE FROM hive.tenant_administration_audit;
        RAISE EXCEPTION 'Runtime can delete critical audit';
    EXCEPTION WHEN insufficient_privilege THEN NULL;
    END;
    IF pg_has_role(current_user,'hive_migrator','MEMBER') OR
       pg_has_role(current_user,'hive_backup','MEMBER') THEN
        RAISE EXCEPTION 'Runtime inherits privileged role';
    END IF;
    IF has_function_privilege(current_user,'hive.purge_critical_audit(text,integer,boolean)','EXECUTE') OR
       has_table_privilege(current_user,'hive.offline_leases','SELECT,INSERT,UPDATE,DELETE') THEN
        RAISE EXCEPTION 'Runtime has Offline or purge privileges';
    END IF;
END
$$;
SET CONSTRAINTS ALL IMMEDIATE;
DO $$
BEGIN
    IF (SELECT count(*) FROM hive.realtime_events WHERE kind='sale.pending') <> 1 THEN
        RAISE EXCEPTION 'Outbox trigger did not emit via runtime grants';
    END IF;
END
$$;
ROLLBACK;
