import { createHash } from "node:crypto";
import { relative, join } from "node:path";
import { fileURLToPath } from "node:url";
import { cp, mkdir, readFile, readdir, rm, writeFile } from "node:fs/promises";

const root = new URL("../", import.meta.url);
const output = new URL("dist/", root);
const configured = process.env.HIVE_APP_API_ORIGIN ?? "";
let origin = "";
if (configured) {
  const parsed = new URL(configured);
  const loopback = ["localhost", "127.0.0.1", "[::1]"].includes(parsed.hostname);
  if (parsed.origin !== configured || !(parsed.protocol === "https:" || (loopback && parsed.protocol === "http:"))) {
    throw new Error("HIVE_APP_API_ORIGIN must be an exact HTTPS origin or an HTTP loopback origin");
  }
  origin = parsed.origin;
}

await rm(output, { recursive: true, force: true });
await mkdir(output, { recursive: true });
await cp(new URL("public/", root), output, { recursive: true });
await cp(new URL("src/", root), new URL("assets/", output), { recursive: true });
await writeFile(new URL("assets/config.mjs", output), `export const apiOrigin = ${JSON.stringify(origin)};\n`);
await writeFile(new URL("_headers", output), `/*
  Content-Security-Policy: default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; manifest-src 'self'; connect-src 'self'${origin ? " " + origin : ""}; worker-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'
  Referrer-Policy: no-referrer
  X-Content-Type-Options: nosniff
/sw.js
  Cache-Control: no-cache
/index.html
  Cache-Control: no-cache
/manifest.webmanifest
  Cache-Control: no-cache
`);
const files = (await readdir(output, { recursive: true, withFileTypes: true }))
  .filter(entry => entry.isFile() && !["_headers", "sw.js"].includes(entry.name))
  .map(entry => relative(fileURLToPath(output), join(entry.parentPath, entry.name)).split("/").map(encodeURIComponent).join("/"))
  .sort();
const hash = createHash("sha256");
for (const path of files) {
  hash.update(path).update("\0").update(await readFile(new URL(path, output)));
}
const worker = await readFile(new URL("scripts/worker.js", root), "utf8");
await writeFile(new URL("sw.js", output), worker
  .replace("__CACHE_VERSION__", hash.digest("hex").slice(0, 16))
  .replace("__STATIC_PATHS__", JSON.stringify(files.map(path => "/" + path))));
console.log(`Built ${files.length} static shell assets`);
