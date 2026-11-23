import { readdir, readFile } from "node:fs/promises";
import { execFileSync } from "node:child_process";
import assert from "node:assert/strict";
for (const root of ["src", "scripts", "test"]) {
  for (const entry of await readdir(root, { recursive: true, withFileTypes: true })) {
    if (entry.isFile() && /\.m?js$/.test(entry.name)) {
      execFileSync(process.execPath, ["--check", `${entry.parentPath}/${entry.name}`], { stdio: "inherit" });
    }
  }
}
const wrangler = await readFile("wrangler.example.toml", "utf8");
assert.match(wrangler, /^\[assets\]$/m);
assert.doesNotMatch(wrangler, /^\s*(main|run_worker_first)\s*=/m);
