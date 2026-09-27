// Resolves the envinfo schema family from schemas/index.json with ajv, offline.
// Every schema is registered by its catalog id; no loadSchema is configured, so
// an unresolved $ref fails compilation instead of being fetched.
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import Ajv2020 from "ajv/dist/2020.js";
import addFormats from "ajv-formats";

const repo = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../..");
const rootId = "contract://sumpter.envinfo/v0.1.0/complete.schema.json";

const catalog = JSON.parse(readFileSync(path.join(repo, "schemas/index.json"), "utf8"));
const envinfo = catalog.resources.filter((r) => r.id.startsWith("contract://sumpter.envinfo/"));
if (envinfo.length === 0) {
  throw new Error("catalog lists no envinfo resources");
}

const ajv = new Ajv2020({ strict: true });
addFormats(ajv);
for (const resource of envinfo) {
  const doc = JSON.parse(readFileSync(path.join(repo, "schemas", resource.path), "utf8"));
  if (doc.$id !== resource.id) {
    throw new Error(`${resource.path}: $id ${doc.$id} does not match catalog id ${resource.id}`);
  }
  ajv.addSchema(doc);
}

const validate = ajv.getSchema(rootId) ?? (() => {
  throw new Error(`catalog has no ${rootId}`);
})();

const fixture = JSON.parse(readFileSync(path.join(repo, "tests/fixtures/envinfo/complete.json"), "utf8"));
if (!validate(fixture)) {
  console.error(validate.errors);
  throw new Error("fixture failed validation");
}

// A rule defined in a sibling schema proves the sibling reference resolved.
const invalid = structuredClone(fixture);
invalid.xml.maxMemoryTarget = "unbounded";
if (validate(invalid)) {
  throw new Error("document violating a sibling-schema rule validated");
}

console.log(`ajv resolved ${envinfo.length} envinfo resources from the catalog: valid fixture passed, invalid fixture rejected`);
