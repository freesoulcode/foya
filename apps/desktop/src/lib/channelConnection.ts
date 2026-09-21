import type { MessagingChannelSettings } from "@/lib/api";

export function isConfiguredChannel(
  channel: MessagingChannelSettings
): boolean {
  if (channel.kind === "feishu") {
    return Boolean(channel.app_id && channel.has_app_secret);
  }
  return channel.has_token;
}
