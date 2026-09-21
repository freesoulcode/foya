import { describe, expect, it } from "vitest";
import type {
  FeishuChannelSettings,
  TelegramChannelSettings,
} from "@/lib/api";
import { isConfiguredChannel } from "@/lib/channelConnection";

const base = {
  name: "Bot",
  locale: "zh-CN" as const,
  enabled: false,
  approval_mode: "auto" as const,
  allowed_users: [],
  allowed_chats: [],
  allow_all: false,
  status: "stopped" as const,
};

describe("isConfiguredChannel", () => {
  it("rejects legacy empty Feishu placeholders", () => {
    const channel: FeishuChannelSettings = {
      ...base,
      id: "feishu-empty",
      kind: "feishu",
      app_id: "",
      has_app_secret: false,
    };

    expect(isConfiguredChannel(channel)).toBe(false);
  });

  it("accepts complete Feishu and Telegram connections", () => {
    const feishu: FeishuChannelSettings = {
      ...base,
      id: "feishu-ready",
      kind: "feishu",
      app_id: "cli_ready",
      has_app_secret: true,
    };
    const telegram: TelegramChannelSettings = {
      ...base,
      id: "telegram-ready",
      kind: "telegram",
      has_token: true,
    };

    expect(isConfiguredChannel(feishu)).toBe(true);
    expect(isConfiguredChannel(telegram)).toBe(true);
  });
});
