import { createRouter, createWebHashHistory } from "vue-router";
import AppLayout from "@/layouts/AppLayout.vue";

export const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    {
      path: "/",
      component: AppLayout,
      children: [
        { path: "", redirect: { name: "chat" } },
        {
          path: "chat",
          component: () => import("@/layouts/ChatLayout.vue"),
          children: [
            {
              path: "",
              name: "chat",
              component: () => import("@/views/ChatView.vue"),
            },
          ],
        },
        {
          path: "studio",
          component: () => import("@/layouts/StudioLayout.vue"),
          children: [
            {
              path: "",
              name: "studio",
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
              name: "automations",
              component: () => import("@/views/AutomationsView.vue"),
            },
          ],
        },
        {
          path: "settings",
          component: () => import("@/layouts/SettingsLayout.vue"),
          children: [
            { path: "", redirect: { name: "settings-connections" } },
            {
              path: "connections",
              name: "settings-connections",
              component: () => import("@/views/settings/ConnectionsView.vue"),
              meta: { settingsSection: "connections" },
            },
            {
              path: "default-models",
              name: "settings-default-models",
              component: () => import("@/views/settings/DefaultModelsView.vue"),
              meta: { settingsSection: "default-models" },
            },
            {
              path: "rules",
              name: "settings-rules",
              component: () => import("@/views/settings/RulesView.vue"),
              meta: { settingsSection: "rules" },
            },
            {
              path: "memory",
              name: "settings-memory",
              component: () => import("@/views/settings/MemoryView.vue"),
              meta: { settingsSection: "memory" },
            },
            {
              path: "skills",
              name: "settings-skills",
              component: () => import("@/views/settings/SkillsView.vue"),
              meta: { settingsSection: "skills" },
            },
            {
              path: "commands",
              name: "settings-commands",
              component: () => import("@/views/settings/CommandsView.vue"),
              meta: { settingsSection: "commands" },
            },
            {
              path: "hooks",
              name: "settings-hooks",
              component: () => import("@/views/settings/HooksView.vue"),
              meta: { settingsSection: "hooks" },
            },
            {
              path: "mcp",
              name: "settings-mcp",
              component: () => import("@/views/settings/McpView.vue"),
              meta: { settingsSection: "mcp" },
            },
            {
              path: "web-search",
              name: "settings-web-search",
              component: () => import("@/views/settings/WebSearchView.vue"),
              meta: { settingsSection: "web-search" },
            },
            {
              path: "channels",
              name: "settings-channels",
              component: () => import("@/views/settings/ChannelsView.vue"),
              meta: { settingsSection: "channels" },
            },
            { path: "feishu-bot", redirect: { name: "settings-channels" } },
            {
              path: "agents",
              name: "settings-agents",
              component: () => import("@/views/settings/AgentsView.vue"),
              meta: { settingsSection: "agents" },
            },
            {
              path: "usage",
              name: "settings-usage",
              component: () => import("@/views/settings/UsageView.vue"),
              meta: { settingsSection: "usage" },
            },
            {
              path: "appearance",
              name: "settings-appearance",
              component: () => import("@/views/settings/AppearanceView.vue"),
              meta: { settingsSection: "appearance" },
            },
          ],
        },
      ],
    },
    { path: "/:pathMatch(.*)*", redirect: { name: "chat" } },
  ],
});
