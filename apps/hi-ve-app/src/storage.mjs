const name = "hi-ve-app";
const version = 1;
let opening;

export function openStorage() {
  if (opening) return opening;
  opening = new Promise((resolve, reject) => {
    const request = indexedDB.open(name, version);
    request.onupgradeneeded = () => {
      const db = request.result;
      db.createObjectStore("meta");
      const sales = db.createObjectStore("sales", { keyPath: "id" });
      sales.createIndex("scope", ["tenant", "device", "outlet", "operator"]);
      db.createObjectStore("login_events", { keyPath: "id" });
    };
    request.onerror = () => reject(request.error);
    request.onblocked = () => reject(new Error("Pembaruan penyimpanan terblokir oleh tab lain"));
    request.onsuccess = () => {
      const db = request.result;
      db.onversionchange = () => { db.close(); opening = undefined; };
      db.onclose = () => { opening = undefined; };
      resolve(db);
    };
  }).catch(error => { opening = undefined; throw error; });
  return opening;
}

async function transact(store, mode, work) {
  const db = await openStorage();
  return new Promise((resolve, reject) => {
    const tx = db.transaction(store, mode, mode === "readwrite" ? { durability: "strict" } : {});
    let value;
    tx.oncomplete = () => resolve(value);
    tx.onabort = () => reject(tx.error ?? new Error("Penyimpanan tidak selesai"));
    tx.onerror = () => reject(tx.error ?? new Error("Penyimpanan gagal"));
    try {
      work(tx.objectStore(store), result => { value = result; });
    } catch (error) {
      tx.abort();
      reject(error);
    }
  });
}

export function readMeta(key) {
  return transact("meta", "readonly", (store, done) => {
    const request = store.get(key);
    request.onsuccess = () => done(request.result);
  });
}

export function writeMeta(key, value) {
  return transact("meta", "readwrite", store => store.put(value, key));
}

export function listCash(scope) {
  return transact("sales", "readonly", (store, done) => {
    const request = store.index("scope").getAll([scope.tenant, scope.device, scope.outlet, scope.operator]);
    request.onsuccess = () => done(request.result.sort((a, b) => b.recordedAt.localeCompare(a.recordedAt)));
  });
}

export function addCash(record) {
  // add keeps an existing document intact even when IDs collide or a tab retries.
  return transact("sales", "readwrite", store => store.add(record));
}

export function updateCash(id, apply) {
  return transact("sales", "readwrite", (store, done) => {
    const request = store.get(id);
    request.onsuccess = () => {
      const original = request.result;
      if (!original) return;
      // A stale tab cannot downgrade an acknowledged sale.
      const next = original.state === "synced" ? original : apply(original);
      store.put(next);
      done(next);
    };
  });
}

export function clearLockedSession(expected) {
  return transact("meta", "readwrite", store => {
    const read = store.get("session");
    read.onsuccess = () => {
      const pointer = read.result;
      if (pointer?.locked && pointer.tenant === expected.tenant && pointer.device === expected.device &&
          pointer.context === expected.context && pointer.revision === expected.revision) store.put(null, "session");
    };
  });
}
