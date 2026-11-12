import fs from "fs"; import path from "path"; import { createRequire } from "module";
// Usage: node conformance/runner/xcheck-ajv.mjs <repo root> <dir where ajv@8.17.1 is installed>
// Cross-checks schema-only verdicts of document cases with a second validator (ADR-0008).
const require = createRequire(process.argv[3] + "/");
const Ajv2020 = require("ajv/dist/2020").default;
const root = process.argv[2];
const ajv = new Ajv2020({ strict: false, allErrors: true });
const walk = d => fs.readdirSync(d, { withFileTypes: true }).flatMap(e => e.isDirectory() ? walk(path.join(d, e.name)) : [path.join(d, e.name)]);
for (const f of walk(path.join(root, "schemas")).concat([path.join(root, "conformance/case.schema.json")])) ajv.addSchema(JSON.parse(fs.readFileSync(f)));
let agree = 0, disagree = 0, skipped = 0;
for (const f of walk(path.join(root, "conformance/cases"))) {
  const c = JSON.parse(fs.readFileSync(f));
  if (!ajv.validate("urn:hcs:schema:conformance:case:0.1", c)) { console.log("CASE-SHAPE", f); disagree++; continue; }
  if (c.level !== "document") { skipped++; continue; }
  const codes = (c.expect.errors || []).map(e => e.code);
  // schema-only verdict expected: invalid only when the expected error is manifest_invalid
  const expectSchemaValid = !codes.includes("manifest_invalid");
  if (codes.includes("unsupported_hcs_version") || f.includes("type-code-mismatch") || f.includes("duplicate-keyid")) { skipped++; continue; } // runner-only semantics
  const ok = ajv.validate(c.target, c.input);
  if (ok === expectSchemaValid) agree++; else { disagree++; console.log("DISAGREE", f, JSON.stringify(ajv.errors).slice(0, 200)); }
}
console.log(`ajv 8.17.1 (draft 2020-12): agree ${agree}, disagree ${disagree}, skipped ${skipped}`);
