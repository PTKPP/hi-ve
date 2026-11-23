package realtime

import (
	"context"
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/jackc/pgx/v5"
	"hi-ve/backend/internal/identity"
	"hi-ve/backend/internal/platform/entitlements"
	"hi-ve/backend/internal/settings"
)

var ErrPushUnavailable = errors.New("web push unavailable")
var ErrPushInput = errors.New("invalid web push subscription")

type PushConfig struct {
	Public  string
	Private string
	Subject string
	// Send is injected by disposable tests. Production uses configured VAPID.
	Send func(context.Context, PushSubscription, []byte) (int, error)
}
type PushSubscription struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

func decodePushKey(raw string) ([]byte, error) {
	return base64.RawURLEncoding.Strict().DecodeString(strings.TrimRight(raw, "="))
}
func allowedPushEndpoint(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.Host != u.Hostname() || u.Path == "" {
		return false
	}
	switch u.Hostname() {
	case "fcm.googleapis.com", "updates.push.services.mozilla.com", "web.push.apple.com":
		return true
	}
	return false
}
func validSubscription(sub PushSubscription) bool {
	auth, err := decodePushKey(sub.Keys.Auth)
	if err != nil || len(auth) != 16 {
		return false
	}
	binary, err := decodePushKey(sub.Keys.P256dh)
	if err != nil {
		return false
	}
	_, err = ecdh.P256().NewPublicKey(binary)
	return err == nil && allowedPushEndpoint(sub.Endpoint)
}

func (c PushConfig) Configured() bool {
	if c.Send != nil {
		return true
	}
	pub, e := decodePushKey(c.Public)
	priv, ee := decodePushKey(c.Private)
	subject, subjectErr := mail.ParseAddress(c.Subject)
	if e != nil || ee != nil || subjectErr != nil || subject.Address != c.Subject {
		return false
	}
	k, e := ecdh.P256().NewPrivateKey(priv)
	return e == nil && string(k.PublicKey().Bytes()) == string(pub)
}

func (s Service) PushKey(ctx context.Context, tenant, token string) (string, error) {
	if !s.Push.Configured() {
		return "", ErrPushUnavailable
	}
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		_, _, _, err := s.pushAuthorization(ctx, tx, tenant, token)
		return err
	})
	return s.Push.Public, err
}
func (s Service) pushAuthorization(ctx context.Context, tx pgx.Tx, tenant, token string) (string, int64, int64, error) {
	actor, err := identity.AuthorizeTenantOwner(ctx, tx, tenant, token)
	if err != nil {
		return "", 0, 0, err
	}
	grant, err := s.Entitlements.CheckWriteSnapshot(ctx, tx, tenant, "realtime.notifications")
	if err != nil {
		return "", 0, 0, err
	}
	setting, err := settings.ReadWebPush(ctx, tx)
	if err != nil {
		return "", 0, 0, err
	}
	if !setting.Enabled {
		return "", 0, 0, ErrPushUnavailable
	}
	return actor, setting.Revision, grant.Revision, nil
}
func (s Service) Subscribe(ctx context.Context, tenant, token string, sub PushSubscription) error {
	if !s.Push.Configured() {
		return ErrPushUnavailable
	}
	if !validSubscription(sub) {
		return ErrPushInput
	}
	return pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		actor, rev, entitlementRev, err := s.pushAuthorization(ctx, tx, tenant, token)
		if err != nil {
			return err
		}
		// Allow replacement from the same owner for an intentional re-enrollment.
		// Do not transfer an endpoint across tenants or principals.
		tag, err := tx.Exec(ctx, `INSERT INTO hive.web_push_subscriptions(endpoint,tenant_id,actor_id,session_digest,p256dh,auth,last_sequence,setting_revision,entitlement_revision)
   VALUES($1,$2,$3,$4,$5,$6,COALESCE((SELECT last_event FROM hive.realtime_streams WHERE tenant_id=$2),0),$7,$8)
   ON CONFLICT(endpoint) DO UPDATE SET session_digest=EXCLUDED.session_digest,p256dh=EXCLUDED.p256dh,auth=EXCLUDED.auth,
   setting_revision=EXCLUDED.setting_revision,entitlement_revision=EXCLUDED.entitlement_revision,
   last_sequence=CASE WHEN hive.web_push_subscriptions.setting_revision=EXCLUDED.setting_revision
     AND hive.web_push_subscriptions.entitlement_revision=EXCLUDED.entitlement_revision THEN hive.web_push_subscriptions.last_sequence ELSE EXCLUDED.last_sequence END
   WHERE (hive.web_push_subscriptions.tenant_id,hive.web_push_subscriptions.actor_id)=(EXCLUDED.tenant_id,EXCLUDED.actor_id)`,
			sub.Endpoint, tenant, actor, identity.Digest(token), sub.Keys.P256dh, sub.Keys.Auth, rev, entitlementRev)
		if err == nil && tag.RowsAffected() == 0 {
			return identity.ErrForbidden
		}
		return err
	})
}
func (s Service) Unsubscribe(ctx context.Context, tenant, token, endpoint string) error {
	if !allowedPushEndpoint(endpoint) {
		return ErrPushInput
	}
	return pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		actor, err := identity.AuthorizeTenantOwner(ctx, tx, tenant, token)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM hive.web_push_subscriptions WHERE endpoint=$1 AND tenant_id=$2 AND actor_id=$3 AND session_digest=$4`, endpoint, tenant, actor, identity.Digest(token))
		return err
	})
}

type pushJob struct {
	Endpoint, Tenant, Actor         string
	Sequence, Revision, Entitlement int64
	Digest                          []byte
}

// Claim after commit, with a durable cursor per subscription. Ambiguous
// sends are not retried: in-app replay remains the durable notification path.
func (s Service) claimPush(ctx context.Context) (pushJob, error) {
	var out pushJob
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		setting, err := settings.ReadWebPush(ctx, tx)
		if err != nil {
			return err
		}
		if !setting.Enabled {
			return nil
		}
		err = tx.QueryRow(ctx, `SELECT p.endpoint,p.tenant_id,p.actor_id,e.sequence,p.setting_revision,p.entitlement_revision,p.session_digest
  FROM hive.web_push_subscriptions p
  JOIN hive.tenant_entitlements c ON c.tenant_id=p.tenant_id AND c.capability_id='realtime.notifications'
   AND c.revision=p.entitlement_revision AND c.revoked_at IS NULL AND c.effective_at<=clock_timestamp() AND c.expires_at>clock_timestamp()
  JOIN hive.active_tenants t ON t.id=p.tenant_id
  JOIN hive.tenant_sessions u ON u.digest=p.session_digest AND u.expires_at>clock_timestamp()
  JOIN LATERAL (SELECT sequence FROM hive.realtime_events
    WHERE tenant_id=p.tenant_id AND kind='sale.received' AND sequence>p.last_sequence ORDER BY sequence LIMIT 1) e ON true
  WHERE p.setting_revision=$1 ORDER BY p.last_sequence,p.endpoint FOR UPDATE OF p SKIP LOCKED LIMIT 1`, setting.Revision).
			Scan(&out.Endpoint, &out.Tenant, &out.Actor, &out.Sequence, &out.Revision, &out.Entitlement, &out.Digest)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE hive.web_push_subscriptions SET last_sequence=$2 WHERE endpoint=$1 AND last_sequence<$2 AND session_digest=$3`,
			out.Endpoint, out.Sequence, out.Digest)
		if err == nil && tag.RowsAffected() == 0 {
			out = pushJob{}
		}
		return err
	})
	return out, err
}
func (s Service) DeliverPush(ctx context.Context) (bool, error) {
	if !s.Push.Configured() {
		return false, nil
	}
	job, err := s.claimPush(ctx)
	if err != nil || job.Endpoint == "" {
		return false, err
	}
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		// Lock in Identity order, before the session and the subscription. This
		// makes revocation and delivery serialize without holding the sales outbox.
		var member string
		err := tx.QueryRow(ctx, `SELECT principal_id FROM hive.tenant_memberships WHERE tenant_id=$1 AND principal_id=$2
   AND active AND role='primary_owner' FOR SHARE`, job.Tenant, job.Actor).Scan(&member)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var digest []byte
		err = tx.QueryRow(ctx, `SELECT u.digest FROM hive.tenant_sessions u JOIN hive.web_push_subscriptions p ON p.session_digest=u.digest
  WHERE u.tenant_id=$1 AND u.principal_id=$2 AND p.endpoint=$3 AND u.auth_method='google' AND u.expires_at>clock_timestamp() AND u.digest=$4
  FOR SHARE OF u`, job.Tenant, member, job.Endpoint, job.Digest).Scan(&digest)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var tenant string
		err = tx.QueryRow(ctx, `SELECT id FROM hive.active_tenants WHERE id=$1 FOR SHARE`, job.Tenant).Scan(&tenant)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		grant, err := s.Entitlements.CheckWriteSnapshot(ctx, tx, tenant, "realtime.notifications")
		if errors.Is(err, entitlements.ErrDenied) {
			return nil
		}
		if err != nil {
			return err
		}
		if grant.Revision != job.Entitlement {
			return nil
		}
		setting, err := settings.ReadWebPush(ctx, tx)
		if err != nil {
			return err
		}
		if !setting.Enabled || setting.Revision != job.Revision {
			return nil
		}
		var sub PushSubscription
		err = tx.QueryRow(ctx, `SELECT endpoint,p256dh,auth FROM hive.web_push_subscriptions
    WHERE endpoint=$1 AND tenant_id=$2 AND actor_id=$3 AND session_digest=$4 AND setting_revision=$5 FOR UPDATE`,
			job.Endpoint, tenant, member, digest, job.Revision).Scan(&sub.Endpoint, &sub.Keys.P256dh, &sub.Keys.Auth)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if !validSubscription(sub) {
			return ErrPushInput
		}
		event := sha256.Sum256([]byte(tenant + ":" + strconv.FormatInt(job.Sequence, 10)))
		payload, _ := json.Marshal(map[string]string{"id": hex.EncodeToString(event[:]), "type": "sale.received"})
		sendCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		status, err := s.Push.send(sendCtx, sub, payload)
		if status == 404 || status == 410 {
			_, err = tx.Exec(ctx, `DELETE FROM hive.web_push_subscriptions WHERE endpoint=$1`, sub.Endpoint)
		}
		return err
	})
	return true, err
}

func (c PushConfig) send(ctx context.Context, s PushSubscription, payload []byte) (int, error) {
	if c.Send != nil {
		return c.Send(ctx, s, payload)
	}
	client := &http.Client{Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	sub := webpush.Subscription{Endpoint: s.Endpoint, Keys: webpush.Keys{P256dh: s.Keys.P256dh, Auth: s.Keys.Auth}}
	res, err := webpush.SendNotificationWithContext(ctx, payload, &sub, &webpush.Options{
		HTTPClient: client, Subscriber: c.Subject, VAPIDPublicKey: c.Public, VAPIDPrivateKey: c.Private, TTL: 0,
	})
	if err != nil {
		return 0, errors.New("web push delivery failed")
	}
	defer res.Body.Close()
	if res.StatusCode != 404 && res.StatusCode != 410 && (res.StatusCode < 200 || res.StatusCode >= 300) {
		return res.StatusCode, errors.New("web push delivery rejected")
	}
	return res.StatusCode, nil
}
