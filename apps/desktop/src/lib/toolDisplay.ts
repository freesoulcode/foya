import type { ToolCallView } from "@/lib/api";

export function parseToolPayload(
  raw: unknown,
): Record<string, unknown> | null {
  let value = raw;
  for (let depth = 0; depth < 2 && typeof value === "string"; depth++) {
    try {
      value = JSON.parse(value);
    } catch {
      return null;
    }
  }
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;
}

export function formatToolInput(input: unknown): string {
  if (typeof input !== "string") {
    try {
      return JSON.stringify(input, null, 2);
    } catch {
      return String(input);
    }
  }
  try {
    return JSON.stringify(JSON.parse(input), null, 2);
  } catch {
    return input;
  }
}

export function commandText(tool: ToolCallView): string {
  if (!tool.input) return "";
  const value = parseToolPayload(tool.input);
  if (typeof value?.command === "string") return value.command;
  return formatToolInput(tool.input);
}

export function isBackgroundCommandEnvelope(output?: string): boolean {
  if (!output) return false;
  let value: Record<string, unknown>;
  try {
    const parsed = JSON.parse(output) as unknown;
    if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
      return false;
    }
    value = parsed as Record<string, unknown>;
  } catch {
    return false;
  }
  const command = value?.command;
  return (
    value?.status === "running_in_background" &&
    (value.initiated_by === "agent" || value.initiated_by === "user") &&
    typeof value.message === "string" &&
    command !== null &&
    typeof command === "object" &&
    !Array.isArray(command) &&
    typeof (command as Record<string, unknown>).command_id === "string" &&
    typeof (command as Record<string, unknown>).session_id === "string" &&
    typeof (command as Record<string, unknown>).command === "string" &&
    typeof (command as Record<string, unknown>).running === "boolean" &&
    typeof (command as Record<string, unknown>).started_at === "string"
  );
}

export function commandOutput(tool: ToolCallView): string {
  if (!tool.output || isBackgroundCommandEnvelope(tool.output)) return "";
  return tool.output.replace(/\s+$/, "");
}
