import { cp, mkdir, rm, writeFile } from "node:fs/promises";

for (const app of ["landing", "superadmin"]) {
  const root = new URL("../" + app + "/", import.meta.url);
  const out = new URL("dist/", root);
  await rm(out, { recursive: true, force: true });
  await mkdir(out, { recursive: true });
  await cp(new URL("public/", root),out,{recursive:true});
  if (app === "landing") continue;
  const origin = process.env.HIVE_SUPERADMIN_API_ORIGIN || "";
  if (origin) {
    const parsed = new URL(origin);
    const loopback = ["localhost","127.0.0.1","[::1]"].includes(parsed.hostname);
    if (parsed.origin !== origin || !(parsed.protocol === "https:" || loopback && parsed.protocol === "http:")) {
      throw new Error("HIVE_SUPERADMIN_API_ORIGIN harus berupa origin HTTPS atau HTTP loopback.");
    }
  }
  await cp(new URL("src/",root),new URL("assets/",out),{recursive:true});
  await writeFile(new URL("assets/config.mjs",out), "export const origin = " + JSON.stringify(origin) + ";\n");
  await writeFile(new URL("_headers",out), `/*
  Content-Security-Policy: default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'${origin ? " "+origin : ""}; frame-ancestors 'none'; base-uri 'none'; form-action 'none'
  Cache-Control: no-store
  Referrer-Policy: no-referrer
  X-Content-Type-Options: nosniff
`);
}
