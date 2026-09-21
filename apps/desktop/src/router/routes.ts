import type { RouteRecordRaw } from "vue-router";
import AppLayout from "@/layouts/AppLayout.vue";
import { RouteName } from "@/router/navigation";

const settingsRoutes: RouteRecordRaw[] = [
  { path: "", redirect: { name: RouteName.settingsConnections } },
  {
    path: "kernel",
    name: RouteName.settingsKernel,
    component: () => import("@/views/settings/KernelView.vue"),
    meta: { settingsSection: "kernel" },
  },
  {
    path: "connections",
    name: RouteName.settingsConnections,
    component: () => import("@/views/settings/ConnectionsView.vue"),
    meta: { settingsSection: "connections" },
  },
  {
    path: "default-models",
    name: RouteName.settingsDefaultModels,
    component: () => import("@/views/settings/DefaultModelsView.vue"),
    meta: { settingsSection: "default-models" },
  },
  {
    path: "rules",
    name: RouteName.settingsRules,
    component: () => import("@/views/settings/RulesView.vue"),
    meta: { settingsSection: "rules" },
  },
  {
    path: "memory",
    name: RouteName.settingsMemory,
    component: () => import("@/views/settings/MemoryView.vue"),
    meta: { settingsSection: "memory" },
  },
  {
    path: "skills",
    name: RouteName.settingsSkills,
    component: () => import("@/views/settings/SkillsView.vue"),
    meta: { settingsSection: "skills" },
  },
  {
    path: "commands",
    name: RouteName.settingsCommands,
    component: () => import("@/views/settings/CommandsView.vue"),
    meta: { settingsSection: "commands" },
  },
  {
    path: "hooks",
    name: RouteName.settingsHooks,
    component: () => import("@/views/settings/HooksView.vue"),
    meta: { settingsSection: "hooks" },
  },
  {
    path: "mcp",
    name: RouteName.settingsMcp,
    component: () => import("@/views/settings/McpView.vue"),
    meta: { settingsSection: "mcp" },
  },
  {
    path: "plugins",
    redirect: { name: RouteName.plugins },
  },
  {
    path: "web-search",
    name: RouteName.settingsWebSearch,
    component: () => import("@/views/settings/WebSearchView.vue"),
    meta: { settingsSection: "web-search" },
  },
  {
    path: "agents",
    name: RouteName.settingsAgents,
    component: () => import("@/views/settings/AgentsView.vue"),
    meta: { settingsSection: "agents" },
  },
  {
    path: "usage",
    name: RouteName.settingsUsage,
    component: () => import("@/views/settings/UsageView.vue"),
    meta: { settingsSection: "usage" },
  },
  {
    path: "appearance",
    name: RouteName.settingsAppearance,
    component: () => import("@/views/settings/AppearanceView.vue"),
    meta: { settingsSection: "appearance" },
  },
];

export const routes: RouteRecordRaw[] = [
  {
    path: "/",
    component: AppLayout,
    children: [
      { path: "", redirect: { name: RouteName.chat } },
      {
        path: "chat",
        component: () => import("@/layouts/ChatLayout.vue"),
        children: [
          {
            path: "new",
            name: RouteName.chatDraft,
            component: () => import("@/views/ChatView.vue"),
          },
          {
            path: ":sessionId?",
            name: RouteName.chat,
            component: () => import("@/views/ChatView.vue"),
          },
        ],
      },
      {
        path: "studio",
        component: () => import("@/layouts/StudioLayout.vue"),
        children: [
          {
            path: ":canvasId?",
            name: RouteName.studio,
            component: () => import("@/views/StudioView.vue"),
          },
        ],
      },
      {
        path: "automations",
        component: () => import("@/layouts/ChatLayout.vue"),
        children: [
          {
            path: "",
            name: RouteName.automations,
            component: () => import("@/views/AutomationsView.vue"),
          },
        ],
      },
      {
        path: "plugins",
        component: () => import("@/layouts/ChatLayout.vue"),
        children: [
          {
            path: "",
            name: RouteName.plugins,
            component: () => import("@/views/PluginsView.vue"),
          },
          {
            path: ":marketplace/:plugin",
            name: RouteName.pluginDetail,
            component: () => import("@/views/PluginDetailView.vue"),
          },
        ],
      },
      {
        path: "settings",
        component: () => import("@/layouts/SettingsLayout.vue"),
        children: settingsRoutes,
      },
    ],
  },
  { path: "/:pathMatch(.*)*", redirect: { name: RouteName.chat } },
];
