import { access, readdir, readFile } from "node:fs/promises";
import { dirname, extname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const siteRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const repositoryRoot = resolve(siteRoot, "../..");
const docsRoot = resolve(siteRoot, "src/content/docs");
const errors = [];

async function markdownFiles(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  const nested = await Promise.all(
    entries.map(async (entry) => {
      const path = resolve(directory, entry.name);
      if (entry.isDirectory()) return markdownFiles(path);
      return extname(entry.name) === ".md" ? [path] : [];
    }),
  );
  return nested.flat().sort();
}

function displayPath(path) {
  return path.slice(repositoryRoot.length + 1);
}

function frontmatterValue(frontmatter, field) {
  const match = frontmatter.match(new RegExp(`^${field}:\\s*(.+)$`, "m"));
  return match?.[1].trim().replace(/^(['"])(.*)\1$/, "$2") ?? "";
}

async function checkLocalTarget(sourcePath, destination, line) {
  if (
    !destination ||
    destination.startsWith("#") ||
    destination.startsWith("/") ||
    /^[a-z][a-z\d+.-]*:/i.test(destination)
  ) {
    return;
  }

  const pathPart = destination.split(/[?#]/, 1)[0];
  if (!pathPart) return;

  let decodedPath;
  try {
    decodedPath = decodeURIComponent(pathPart);
  } catch {
    errors.push(`${displayPath(sourcePath)}:${line}: invalid URL encoding in ${destination}`);
    return;
  }

  try {
    await access(resolve(dirname(sourcePath), decodedPath));
  } catch {
    errors.push(`${displayPath(sourcePath)}:${line}: missing local target ${destination}`);
  }
}

async function checkMarkdown(path, requireFrontmatter) {
  const content = await readFile(path, "utf8");
  const lines = content.split(/\r?\n/);
  let frontmatter = "";

  if (requireFrontmatter) {
    const match = content.match(/^---\r?\n([\s\S]*?)\r?\n---(?:\r?\n|$)/);
    if (!match) {
      errors.push(`${displayPath(path)}: missing YAML frontmatter`);
    } else {
      frontmatter = match[1];
      for (const field of ["title", "description", "slug"]) {
        if (!frontmatterValue(frontmatter, field)) {
          errors.push(`${displayPath(path)}: frontmatter is missing ${field}`);
        }
      }
    }
  }

  let fence = null;
  for (const [index, line] of lines.entries()) {
    const fenceMatch = line.match(/^(`{3,}|~{3,})(.*)$/);
    if (fenceMatch) {
      if (fence === null) {
        fence = fenceMatch[1][0];
        if (!fenceMatch[2].trim()) {
          errors.push(`${displayPath(path)}:${index + 1}: code fence has no language`);
        }
      } else if (fenceMatch[1][0] === fence) {
        fence = null;
      }
    }

    const destinations = [
      ...line.matchAll(/!?\[[^\]]*]\(([^)\s]+)(?:\s+["'][^)]*)?\)/g),
      ...line.matchAll(/\b(?:href|src)="([^"]+)"/g),
    ];
    for (const match of destinations) {
      await checkLocalTarget(path, match[1].replace(/^<|>$/g, ""), index + 1);
    }
  }

  if (fence !== null) {
    errors.push(`${displayPath(path)}: unclosed code fence`);
  }

  return frontmatterValue(frontmatter, "slug");
}

const docs = await markdownFiles(docsRoot);
const slugs = new Map();
for (const path of docs) {
  const slug = await checkMarkdown(path, true);
  if (!slug) continue;
  if (slugs.has(slug)) {
    errors.push(
      `${displayPath(path)}: duplicate slug ${slug} (also in ${displayPath(slugs.get(slug))})`,
    );
  } else {
    slugs.set(slug, path);
  }
}

await checkMarkdown(resolve(repositoryRoot, "README.md"), false);
await checkMarkdown(resolve(siteRoot, "README.md"), false);

const config = await readFile(resolve(siteRoot, "astro.config.mjs"), "utf8");
const sidebarSlugs = new Set(
  [...config.matchAll(/\bslug:\s*"([^"]+)"/g)].map((match) => match[1]),
);
for (const [slug, path] of slugs) {
  if (!sidebarSlugs.has(slug)) {
    errors.push(`${displayPath(path)}: slug ${slug} is missing from the sidebar`);
  }
}
for (const slug of sidebarSlugs) {
  if (!slugs.has(slug)) {
    errors.push(`apps/site/astro.config.mjs: sidebar references unknown slug ${slug}`);
  }
}

if (errors.length > 0) {
  console.error(errors.join("\n"));
  process.exitCode = 1;
} else {
  console.log(`Documentation checks passed for ${docs.length} pages.`);
}
