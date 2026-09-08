import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
import { gzipSync } from "node:zlib";

const desktopRoot = fileURLToPath(new URL("../", import.meta.url));
const repositoryRoot = fileURLToPath(new URL("../../../", import.meta.url));
const outputRoot = join(desktopRoot, "src-tauri", "binaries", "remote");

mkdirSync(outputRoot, { recursive: true });

for (const [arch, name] of [
  ["amd64", "foya-linux-amd64"],
  ["arm64", "foya-linux-arm64"],
]) {
  const output = join(outputRoot, name);
  const compressed = `${output}.gz`;
  const result = spawnSync(
    "go",
    [
      "build",
      "-trimpath",
      "-ldflags=-s -w",
      "-o",
      output,
      "./cmd/foya",
    ],
    {
      cwd: repositoryRoot,
      env: {
        ...process.env,
        GOOS: "linux",
        GOARCH: arch,
        CGO_ENABLED: "0",
      },
      stdio: "inherit",
    }
  );
  if (result.error) throw result.error;
  if (result.status !== 0) process.exit(result.status ?? 1);
  writeFileSync(compressed, gzipSync(readFileSync(output), { level: 9 }));
  rmSync(output);
}
