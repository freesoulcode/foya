import { readFileSync, readdirSync } from "node:fs";
import { extname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const repositoryRoot = fileURLToPath(new URL("../../../", import.meta.url));
const sourceRoot = fileURLToPath(new URL("../src/", import.meta.url));
const localeFile = join(sourceRoot, "i18n/zh-CN.ts");
const supportedExtensions = new Set([".ts", ".vue", ".css", ".rs", ".go"]);

function walk(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) return walk(path);
    return supportedExtensions.has(extname(entry.name)) ? [path] : [];
  });
}

function collectKeys(source) {
  const keys = [];
  const patterns = [
    /(?:\$t|\bt|translate)\(\s*"([^"]+)"/g,
    /(?:\$t|\bt|translate)\(\s*'([^']+)'/g,
  ];
  for (const pattern of patterns) {
    for (const match of source.matchAll(pattern)) keys.push(match[1]);
  }
  return keys;
}

const localeSource = readFileSync(localeFile, "utf8");
const localeKeys = [
  ...localeSource.matchAll(/^\s*"([^"]+)"\s*:/gm),
  ...localeSource.matchAll(/^\s*'([^']+)'\s*:/gm),
].map((match) => match[1]);
const duplicateKeys = [...new Set(
  localeKeys.filter((key, index) => localeKeys.indexOf(key) !== index)
)];
const knownKeys = new Set(localeKeys);
const missingKeys = new Set();
const unexpectedChinese = [];

const i18nSource = readFileSync(join(sourceRoot, "i18n/index.ts"), "utf8");
const errorKeyBlock = i18nSource.match(
  /const ERROR_KEYS:[\s\S]*?= \{([\s\S]*?)\n\};/
)?.[1] ?? "";
for (const match of errorKeyBlock.matchAll(/:\s*"([^"]+)"/g)) {
  if (!knownKeys.has(match[1])) missingKeys.add(match[1]);
}
for (const constant of ["NATIVE_ERROR_PREFIXES", "RUNTIME_TEXT_PREFIXES"]) {
  const block = i18nSource.match(
    new RegExp(`const ${constant} = \\[([\\s\\S]*?)\\] as const`)
  )?.[1] ?? "";
  for (const match of block.matchAll(/"([^"]+)"/g)) {
    if (!knownKeys.has(match[1])) missingKeys.add(match[1]);
  }
}

const sourceFiles = [
  ...walk(sourceRoot),
  ...walk(join(repositoryRoot, "apps/desktop/src-tauri/src")),
  ...walk(join(repositoryRoot, "internal")),
  ...walk(join(repositoryRoot, "cmd")),
];

for (const file of sourceFiles) {
  const source = readFileSync(file, "utf8");
  const allowsChinese =
    file === localeFile ||
    file === join(repositoryRoot, "internal/channel/feishu/messages.go") ||
    file.endsWith("_test.go");
  if (!allowsChinese && /[\u3400-\u9fff]/u.test(source)) {
    unexpectedChinese.push(relative(repositoryRoot, file));
  }
  if (file === localeFile) continue;
  for (const key of collectKeys(source)) {
    if (!knownKeys.has(key)) missingKeys.add(key);
  }
}

if (duplicateKeys.length || missingKeys.size || unexpectedChinese.length) {
  if (duplicateKeys.length) {
    console.error(`Duplicate zh-CN keys:\n${duplicateKeys.sort().join("\n")}`);
  }
  if (missingKeys.size) {
    console.error(`Missing zh-CN keys:\n${[...missingKeys].sort().join("\n")}`);
  }
  if (unexpectedChinese.length) {
    console.error(
      `Chinese text outside the locale resource:\n${unexpectedChinese.sort().join("\n")}`
    );
  }
  process.exitCode = 1;
}
