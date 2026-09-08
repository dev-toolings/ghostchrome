/**
 * E2E example: drive ghostchrome from the TypeScript SDK against a live site.
 *
 * Attaches to an already-running Chrome via --connect=auto (project policy).
 *
 *   GHOSTCHROME_BIN="$PWD/ghostchrome" bun run sdk/examples/typescript/chronovet.ts [url]
 */
import { createGhostchrome } from "../../typescript/src/index";

const url = process.argv[2] ?? "https://www.chronovet.fr/";
const bin = process.env.GHOSTCHROME_BIN ?? "ghostchrome";
const connect = process.env.GHOSTCHROME_CONNECT ?? "--connect=auto";

const gc = createGhostchrome({ command: bin, flags: [connect, "--stealth"] });

try {
  const nav = await gc.navigate(url);
  console.log(`[${nav.result.status}] ${nav.result.title}`);
  console.log(`url: ${nav.result.url}`);

  const ex = await gc.extract({ level: "skeleton" });
  const refs = ex.result.refs ?? {};
  const entries = Object.entries(refs);
  console.log(`refs: ${entries.length}`);
  for (const [ref, node] of entries.slice(0, 10)) {
    console.log(`  ${ref} ${node.role ?? ""} ${node.name ?? ""}`.trimEnd());
  }
} finally {
  await gc.close();
}
