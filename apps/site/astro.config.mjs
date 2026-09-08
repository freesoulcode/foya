import { defineConfig } from "astro/config";
import starlight from "@astrojs/starlight";
import mermaid from "astro-mermaid";
import tailwindcss from "@tailwindcss/vite";

const base = process.env.BASE_PATH || "/";
const publicBase = base === "/" ? "" : `/${base.replace(/^\/+|\/+$/g, "")}`;

export default defineConfig({
  site: process.env.SITE_URL || "https://freesoulcode.github.io",
  base,
  vite: {
    plugins: [tailwindcss()],
  },
  integrations: [
    mermaid({
      autoTheme: true,
      enableLog: false,
      mermaidConfig: {
        securityLevel: "strict",
        flowchart: {
          htmlLabels: false,
          curve: "linear",
        },
      },
    }),
    starlight({
      title: "Foya",
      description: "开源、本地优先、支持自带模型的个人 Agent 系统。",
      logo: {
        src: "./src/assets/foya-icon.png",
        alt: "Foya",
      },
      favicon: `${publicBase}/foya-icon.png`,
      locales: {
        root: {
          label: "简体中文",
          lang: "zh-CN",
        },
      },
      social: [
        {
          icon: "github",
          label: "GitHub",
          href: "https://github.com/freesoulcode/foya",
        },
      ],
      editLink: {
        baseUrl: "https://github.com/freesoulcode/foya/edit/main/apps/site/",
      },
      pagefind: true,
      lastUpdated: true,
      disable404Route: true,
      tableOfContents: {
        minHeadingLevel: 2,
        maxHeadingLevel: 3,
      },
      sidebar: [
        {
          label: "开始",
          items: [
            { slug: "docs" },
            { slug: "docs/quick-start" },
            { slug: "docs/model-connections" },
            { slug: "docs/projects" },
            { slug: "docs/server-deployment" },
          ],
        },
        {
          label: "架构与运行时",
          items: [
            { slug: "docs/technical" },
            { slug: "docs/technical/architecture" },
            { slug: "docs/technical/kernel-service" },
            { slug: "docs/technical/desktop" },
            { slug: "docs/technical/transport" },
            { slug: "docs/technical/agent-runtime" },
            { slug: "docs/technical/messages" },
            { slug: "docs/technical/session" },
            { slug: "docs/subagents" },
          ],
        },
        {
          label: "上下文与知识",
          items: [
            { slug: "docs/technical/context" },
            { slug: "docs/technical/compaction" },
            { slug: "docs/technical/memory" },
            { slug: "docs/technical/rules" },
          ],
        },
        {
          label: "执行系统",
          items: [
            { slug: "docs/technical/tools" },
            { slug: "docs/technical/permissions" },
            { slug: "docs/file-review" },
            { slug: "docs/technical/hooks" },
            { slug: "docs/technical/commands" },
            { slug: "docs/technical/workflows" },
            { slug: "docs/technical/terminal" },
            { slug: "docs/technical/browser" },
            { slug: "docs/technical/web-search" },
          ],
        },
        {
          label: "扩展与集成",
          items: [
            { slug: "docs/plugins" },
            { slug: "docs/skills" },
            { slug: "docs/mcp" },
            { slug: "docs/technical/providers" },
            { slug: "docs/automation" },
            { slug: "docs/channels" },
          ],
        },
        {
          label: "视觉与媒体",
          items: [
            { slug: "docs/technical/artifacts" },
            { slug: "docs/technical/canvas" },
            { slug: "docs/technical/media-generation" },
          ],
        },
        {
          label: "数据与运维",
          items: [
            { slug: "docs/technical/event-store" },
            { slug: "docs/technical/storage" },
            { slug: "docs/technical/usage" },
            { slug: "docs/technical/telemetry" },
            { slug: "docs/technical/configuration" },
            { slug: "docs/technical/lifecycle" },
            { slug: "docs/technical/security" },
          ],
        },
        {
          label: "参考",
          items: [{ slug: "docs/cli" }],
        },
      ],
      credits: false,
    }),
  ],
});
