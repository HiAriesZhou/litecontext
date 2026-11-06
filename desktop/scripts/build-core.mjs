import { spawnSync } from "node:child_process";
import { mkdirSync } from "node:fs";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../", import.meta.url));
const repo = fileURLToPath(new URL("../../", import.meta.url));
function run(command, args, options = {}) {
  const result = spawnSync(command, args, { encoding: "utf8", ...options });
  if (result.error || result.status !== 0) {
    throw new Error(`${command} failed: ${result.error?.message || result.stderr}`);
  }
  return result.stdout.trim();
}
const target = run("rustc", ["--print", "host-tuple"]);
if (process.platform !== "darwin") {
  throw new Error("This desktop build currently verifies macOS only.");
}
const directory = `${root}src-tauri/binaries`;
mkdirSync(directory, { recursive: true });
run("go", ["build", "-trimpath", "-o", `${directory}/lc-${target}`, "./cmd/litecontext"], {
  cwd: repo,
  env: { ...process.env, CGO_ENABLED: "0" },
});
console.log(`Built Go core for ${target}.`);
