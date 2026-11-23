-- Realtime owns delivery intent and owner notification preferences.
SET LOCAL lock_timeout='5s';
CREATE TABLE hive.realtime_streams (
    tenant_id uuid PRIMARY KEY REFERENCES hive.tenants,
    last_event bigint NOT NULL CHECK(last_event>0)
);
CREATE TABLE hive.realtime_events (
    tenant_id uuid NOT NULL, sequence bigint NOT NULL, outlet_id uuid NOT NULL, sale_id uuid NOT NULL,
    kind text NOT NULL CHECK(kind IN ('sale.received','sale.pending','sale.reversed','sale.refund_recorded')),
    PRIMARY KEY(tenant_id,sequence),
    FOREIGN KEY(tenant_id,outlet_id) REFERENCES hive.locations,
    FOREIGN KEY(tenant_id,sale_id) REFERENCES hive.sales_orders
);
CREATE TABLE hive.realtime_preferences (
    tenant_id uuid NOT NULL, actor_id uuid NOT NULL, sale_notifications boolean NOT NULL,
    PRIMARY KEY(tenant_id,actor_id),
    FOREIGN KEY(tenant_id,actor_id) REFERENCES hive.tenant_memberships
);

-- Deferred triggers also capture commits from older Sales writers. The
-- tenant cursor is allocated at commit, so later commits cannot overtake it.
CREATE FUNCTION hive.realtime_capture_sale() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE
    sale uuid; outlet uuid; event_kind text; event_sequence bigint;
BEGIN
    CASE TG_TABLE_NAME
    WHEN 'sales_orders' THEN
        sale:=NEW.id;
        outlet:=NEW.outlet_id;
        event_kind:=CASE WHEN NEW.method='cash' THEN 'sale.received' ELSE 'sale.pending' END;
    WHEN 'sales_manual_confirmations' THEN
        sale:=NEW.sale_id;
        event_kind:='sale.received';
    WHEN 'sales_reversal_postings' THEN
        SELECT r.sale_id INTO STRICT sale FROM hive.sales_reversal_requests r WHERE (r.tenant_id,r.id)=(NEW.tenant_id,NEW.id);
        event_kind:='sale.reversed';
    WHEN 'sales_refund_confirmations' THEN
        SELECT r.sale_id INTO STRICT sale FROM hive.sales_reversal_requests r WHERE (r.tenant_id,r.id)=(NEW.tenant_id,NEW.reversal_id);
        event_kind:='sale.refund_recorded';
    END CASE;
    IF outlet IS NULL THEN
        SELECT o.outlet_id INTO STRICT outlet FROM hive.sales_orders o WHERE (o.tenant_id,o.id)=(NEW.tenant_id,sale);
    END IF;
    INSERT INTO hive.realtime_streams(tenant_id,last_event) VALUES(NEW.tenant_id,1)
      ON CONFLICT(tenant_id) DO UPDATE SET last_event=hive.realtime_streams.last_event+1
      RETURNING last_event INTO event_sequence;
    INSERT INTO hive.realtime_events VALUES(NEW.tenant_id,event_sequence,outlet,sale,event_kind);
    RETURN NEW;
END $$;
CREATE CONSTRAINT TRIGGER sales_order_event AFTER INSERT ON hive.sales_orders DEFERRABLE INITIALLY DEFERRED
 FOR EACH ROW EXECUTE FUNCTION hive.realtime_capture_sale();
CREATE CONSTRAINT TRIGGER sales_payment_event AFTER INSERT ON hive.sales_manual_confirmations DEFERRABLE INITIALLY DEFERRED
 FOR EACH ROW EXECUTE FUNCTION hive.realtime_capture_sale();
CREATE CONSTRAINT TRIGGER sales_reversal_event AFTER INSERT ON hive.sales_reversal_postings DEFERRABLE INITIALLY DEFERRED
 FOR EACH ROW EXECUTE FUNCTION hive.realtime_capture_sale();
CREATE CONSTRAINT TRIGGER sales_refund_event AFTER INSERT ON hive.sales_refund_confirmations DEFERRABLE INITIALLY DEFERRED
 FOR EACH ROW EXECUTE FUNCTION hive.realtime_capture_sale();
