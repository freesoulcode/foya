import type { RouteLocationRaw } from "vue-router";

export const RouteName = {
  chat: "chat",
  chatDraft: "chat-draft",
  studio: "studio",
  automations: "automations",
  plugins: "plugins",
  pluginDetail: "plugin-detail",
  settingsKernel: "settings-kernel",
  settingsConnections: "settings-connections",
  settingsDefaultModels: "settings-default-models",
  settingsRules: "settings-rules",
  settingsMemory: "settings-memory",
  settingsSkills: "settings-skills",
  settingsCommands: "settings-commands",
  settingsHooks: "settings-hooks",
  settingsMcp: "settings-mcp",
  settingsWebSearch: "settings-web-search",
  settingsChannels: "settings-channels",
  settingsAgents: "settings-agents",
  settingsUsage: "settings-usage",
  settingsAppearance: "settings-appearance",
} as const;

export type SettingsSection =
  | "kernel"
  | "connections"
  | "default-models"
  | "rules"
  | "memory"
  | "skills"
  | "commands"
  | "hooks"
  | "mcp"
  | "web-search"
  | "channels"
  | "agents"
  | "usage"
  | "appearance";

const settingsRouteBySection: Record<SettingsSection, string> = {
  kernel: RouteName.settingsKernel,
  connections: RouteName.settingsConnections,
  "default-models": RouteName.settingsDefaultModels,
  rules: RouteName.settingsRules,
  memory: RouteName.settingsMemory,
  skills: RouteName.settingsSkills,
  commands: RouteName.settingsCommands,
  hooks: RouteName.settingsHooks,
  mcp: RouteName.settingsMcp,
  "web-search": RouteName.settingsWebSearch,
  channels: RouteName.settingsChannels,
  agents: RouteName.settingsAgents,
  usage: RouteName.settingsUsage,
  appearance: RouteName.settingsAppearance,
};

export function chatLocation(sessionId = ""): RouteLocationRaw {
  return sessionId
    ? { name: RouteName.chat, params: { sessionId } }
    : { name: RouteName.chat };
}

export function newChatLocation(): RouteLocationRaw {
  return { name: RouteName.chatDraft };
}

export function studioLocation(canvasId = ""): RouteLocationRaw {
  return canvasId
    ? { name: RouteName.studio, params: { canvasId } }
    : { name: RouteName.studio };
}

export function settingsLocation(section: SettingsSection): RouteLocationRaw {
  return { name: settingsRouteBySection[section] };
}

export function routeParam(value: string | string[] | undefined): string {
  return Array.isArray(value) ? value[0] ?? "" : value ?? "";
}
