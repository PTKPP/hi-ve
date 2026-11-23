const name = "hi-ve-app";
const version = 2;
let opening;

export function openStorage() {
  if (opening) return opening;
  opening = new Promise((resolve, reject) => {
    const request = indexedDB.open(name, version);
    request.onupgradeneeded = event => {
      const db = request.result;
      if (event.oldVersion < 1) {
        db.createObjectStore("meta");
        const sales = db.createObjectStore("sales", { keyPath: "id" });
        sales.createIndex("scope", ["tenant", "device", "outlet", "operator"]);
        db.createObjectStore("login_events", { keyPath: "id" });
      }
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
      const next = ["synced", "manual_refund_recorded"].includes(original.state) ? original : apply(original);
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


export async function writeJournal(expected, journal, storeName, record, replace = false) {
  const db = await openStorage();
  return new Promise((resolve, reject) => {
    const stores = storeName ? ["meta", storeName] : ["meta"];
    const tx = db.transaction(stores, "readwrite", { durability: "strict" });
    const meta = tx.objectStore("meta");
    const read = meta.get("offline-lease");
    read.onsuccess = () => {
      const previous = read.result;
      if ((previous?.id ?? null) !== expected) {
        tx.abort();
        return;
      }
      try {
        meta.put(journal, "offline-lease");
        if (storeName) {
          const store = tx.objectStore(storeName);
          if (replace) store.put(record);
          else store.add(record);
        }
      } catch {
        tx.abort();
        reject(new Error("Penyimpanan jurnal gagal"));
      }
    };
    tx.oncomplete = () => resolve();
    tx.onabort = () => reject(new Error("Jurnal berubah atau penyimpanan gagal"));
    tx.onerror = () => reject(new Error("Penyimpanan jurnal gagal"));
  });
}

export function pendingLogins() {
  return transact("login_events", "readonly", (store, done) => {
    const request = store.getAll();
    request.onsuccess = () => done(request.result);
  });
}

export function deleteLogin(id) {
  return transact("login_events", "readwrite", store => store.delete(id));
}
