// Load once per installation. IndexedDB persists an opaque CryptoKey without exporting it.
export async function pairingKey() {
  const generated = await crypto.subtle.generateKey(
    { name: "ECDSA", namedCurve: "P-256" }, false, ["sign", "verify"]
  );
  const opening = indexedDB.open("hi-ve-device-identity", 1);
  opening.onupgradeneeded = () => opening.result.createObjectStore("keys");
  const db = await new Promise((resolve, reject) => {
    opening.onsuccess = () => resolve(opening.result);
    opening.onerror = () => reject(opening.error);
    opening.onblocked = () => reject(new Error("Device key storage is blocked"));
  });
  const keys = await new Promise((resolve, reject) => {
    const tx = db.transaction("keys", "readwrite");
    const store = tx.objectStore("keys");
    const reading = store.get("pairing");
    let pair;
    reading.onsuccess = () => {
      pair = reading.result ?? generated;
      if (!reading.result) store.add(pair, "pairing");
    };
    tx.oncomplete = () => resolve(pair);
    tx.onabort = () => reject(tx.error ?? new Error("Device key storage aborted"));
  }).finally(() => db.close());
  const { kty, crv, x, y } = await crypto.subtle.exportKey("jwk", keys.publicKey);
  async function sign(claims) {
    const encode = (bytes) => btoa(String.fromCharCode(...bytes))
      .replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/, "");
    const utf8 = new TextEncoder();
    const body = "eyJhbGciOiJFUzI1NiJ9." + encode(utf8.encode(JSON.stringify(claims)));
    const signature = await crypto.subtle.sign({ name: "ECDSA", hash: "SHA-256" }, keys.privateKey, utf8.encode(body));
    return body + "." + encode(new Uint8Array(signature));
  }
  return {
    public_key_jwk: { kty, crv, x, y },
    proof({ pairing_id, nonce, expires_at }, idempotency_key) {
      return sign({ purpose: "hive.device.pair.redeem.v1", pairing_id, nonce, expires_at, idempotency_key });
    },
    challengeProof({ tenant_id, device_id, operation, context_id, expected_revision, command_digest }) {
      return sign({ purpose: "hive.device.challenge.bootstrap.v1", tenant_id, device_id, operation,
        context_id, expected_revision, command_digest, request_id: crypto.randomUUID(), issued_at: new Date().toISOString() });
    },
    operationProof({ challenge_id, nonce, device_revision, expires_at }, { tenant_id, device_id, operation, context_id, expected_revision, command_digest }) {
      return sign({ purpose: "hive.device.operation.v1", challenge_id, nonce, device_revision, expires_at,
        tenant_id, device_id, operation, context_id, expected_revision, command_digest });
    },
  };
}


// Pass the same serialized body to fetch and keep the receipt key on retries.
export async function catalogCommandDigest(method, path, key, body) {
  const bytes = new TextEncoder().encode([method, path, key, body].join("\n"));
  const hash = await crypto.subtle.digest("SHA-256", bytes);
  return Array.from(new Uint8Array(hash), byte => byte.toString(16).padStart(2, "0")).join("");
}
