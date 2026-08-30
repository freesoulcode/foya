export interface DiffStats {
  additions: number;
  deletions: number;
}

export interface FullDiffLine {
  kind: "add" | "delete" | "context";
  text: string;
  lineNumber?: number;
}

function diffHeaderPath(diff: string): string {
  const marker = diff.split("\n").find((line) => line.startsWith("+++ "));
  if (!marker) return "";
  const path = marker.slice(4).trim().replace(/^"|"$/g, "");
  if (!path || path === "/dev/null") return "";
  return path.replace(/^b\//, "").replace(/\\/g, "/");
}

export function diffFilePath(diff: string, projectRoot = ""): string {
  const path = diffHeaderPath(diff);
  if (!path) return "";

  const absolute = path.startsWith("/") || /^[A-Za-z]:\//.test(path);
  if (!absolute) return path.replace(/^\.\/+/, "");

  const root = projectRoot.replace(/\\/g, "/").replace(/\/+$/, "");
  if (!root || !path.toLowerCase().startsWith(`${root.toLowerCase()}/`)) {
    return "";
  }
  return path.slice(root.length + 1);
}

export function diffFileName(diff: string): string {
  const path = diffHeaderPath(diff);
  return path.split("/").pop() || path;
}

export function diffStats(diff: string): DiffStats {
  let additions = 0;
  let deletions = 0;
  for (const line of diff.split("\n")) {
    if (line.startsWith("+++ ") || line.startsWith("--- ")) continue;
    if (line.startsWith("+")) additions += 1;
    else if (line.startsWith("-")) deletions += 1;
  }
  return { additions, deletions };
}

// Projects a unified diff onto the current file so unchanged lines remain visible.
export function fullDiffLines(diff: string, content: string): FullDiffLine[] {
  const addedLines = new Set<number>();
  const deletedBefore = new Map<number, string[]>();
  const historicalLines = new Map<number, string>();
  let newLine = 0;

  for (const line of diff.split("\n")) {
    const hunk = /^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(line);
    if (hunk) {
      newLine = Number(hunk[1]);
      continue;
    }
    if (newLine === 0 || line.startsWith("--- ") || line.startsWith("+++ ")) {
      continue;
    }
    if (line.startsWith("+")) {
      addedLines.add(newLine);
      historicalLines.set(newLine, line.slice(1));
      newLine += 1;
    } else if (line.startsWith("-")) {
      const deleted = deletedBefore.get(newLine) ?? [];
      deleted.push(line.slice(1));
      deletedBefore.set(newLine, deleted);
    } else if (line.startsWith(" ")) {
      historicalLines.set(newLine, line.slice(1));
      newLine += 1;
    }
  }

  const sourceLines = content === "" ? [] : content.split("\n");
  if (sourceLines[sourceLines.length - 1] === "") sourceLines.pop();

  const rows: FullDiffLine[] = [];
  for (let index = 0; index < sourceLines.length; index += 1) {
    const lineNumber = index + 1;
    for (const text of deletedBefore.get(lineNumber) ?? []) {
      rows.push({ kind: "delete", text });
    }
    rows.push({
      kind: addedLines.has(lineNumber) ? "add" : "context",
      text: historicalLines.get(lineNumber) ?? sourceLines[index],
      lineNumber,
    });
  }
  for (const text of deletedBefore.get(sourceLines.length + 1) ?? []) {
    rows.push({ kind: "delete", text });
  }
  return rows;
}
