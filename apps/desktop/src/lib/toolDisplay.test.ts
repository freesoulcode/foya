import { describe, expect, it } from "vitest";
import type { ToolCallView } from "@/lib/api";
import {
  commandOutput,
  commandText,
  isBackgroundCommandEnvelope,
} from "./toolDisplay";

function tool(overrides: Partial<ToolCallView> = {}): ToolCallView {
  return {
    id: "tool-1",
    name: "bash",
    input: "",
    status: "done",
    ...overrides,
  };
}

describe("command tool display", () => {
  it("extracts commands from live, historical, and encoded inputs", () => {
    expect(
      commandText(tool({ input: '{"command":"pnpm test"}' })),
    ).toBe("pnpm test");
    expect(
      commandText(
        tool({
          input: { command: "go test ./..." } as unknown as string,
        }),
      ),
    ).toBe("go test ./...");
    expect(
      commandText(tool({ input: '"{\\"command\\":\\"printf ready\\"}"' })),
    ).toBe("printf ready");
  });

  it("hides only complete background command envelopes", () => {
    const envelope = JSON.stringify({
      status: "running_in_background",
      initiated_by: "agent",
      message: "The command was started in the background by the agent.",
      command: {
        command_id: "cmd-1",
        session_id: "session-1",
        command: "sleep 30",
        running: true,
        started_at: "2026-09-18T12:00:00Z",
      },
    });

    expect(isBackgroundCommandEnvelope(envelope)).toBe(true);
    expect(commandOutput(tool({ output: envelope }))).toBe("");
    expect(
      commandOutput(
        tool({ output: '{"status":"running_in_background"}\n' }),
      ),
    ).toBe('{"status":"running_in_background"}');
    const encodedEnvelope = JSON.stringify(envelope);
    expect(commandOutput(tool({ output: encodedEnvelope }))).toBe(
      encodedEnvelope,
    );
  });

  it("keeps error output when command input is unavailable", () => {
    expect(
      commandOutput(
        tool({
          input: "",
          output: "invalid arguments",
          status: "error",
        }),
      ),
    ).toBe("invalid arguments");
  });
});
