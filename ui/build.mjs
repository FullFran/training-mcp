// Builds every MCP Apps view into one self-contained HTML file that the Go
// server embeds (internal/mcpsrv/apps). The output is committed so `go build`
// and `go test` never need Node; `node build.mjs --check` fails when a
// committed file differs from what the sources build to (CI runs it).

import { build } from "esbuild";
import { readFile, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";

const here = path.dirname(fileURLToPath(import.meta.url));
const outDir = path.join(here, "..", "internal", "mcpsrv", "apps");

// One entry per view: its source directory under src/ and its output file.
const views = [{ src: "rest-timer", out: "rest-timer.html" }];

const check = process.argv.includes("--check");
let stale = 0;

for (const view of views) {
  const dir = path.join(here, "src", view.src);
  const result = await build({
    entryPoints: [path.join(dir, "main.ts")],
    bundle: true,
    format: "esm",
    platform: "browser",
    target: "es2022",
    minify: true,
    legalComments: "none",
    write: false,
    logLevel: "warning",
  });
  // A literal "</script" inside the bundle would end the inline element early.
  const js = result.outputFiles[0].text.replaceAll("</script", "<\\/script").trimEnd();
  const template = await readFile(path.join(dir, "index.html"), "utf8");
  if (!template.includes("<!--SCRIPT-->")) throw new Error(`${view.src}/index.html has no <!--SCRIPT--> marker`);
  const html = template.replace("<!--SCRIPT-->", () => `<script type="module">${js}</script>`);
  const outFile = path.join(outDir, view.out);

  if (check) {
    const committed = await readFile(outFile, "utf8").catch(() => "");
    if (committed !== html) {
      console.error(`${path.relative(process.cwd(), outFile)} is stale: run \`npm run build\` in ui/ and commit it`);
      stale++;
    }
    continue;
  }
  await writeFile(outFile, html);
  console.log(`${view.out}: ${(Buffer.byteLength(html) / 1024).toFixed(1)} KiB`);
}

if (stale > 0) process.exit(1);
