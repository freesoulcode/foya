<script setup lang="ts">
import { computed, type Component } from "vue";
import {
  CoffeeIcon,
  ContainerIcon,
  DatabaseIcon,
  FileArchiveIcon,
  FileCodeIcon,
  FileCogIcon,
  FileIcon,
  FileImageIcon,
  FileJsonIcon,
  FileLockIcon,
  FileTextIcon,
  HashIcon,
  SquareTerminalIcon,
} from "@lucide/vue";

const props = defineProps<{
  path: string;
}>();

interface BadgeIcon {
  kind: "badge";
  label: string;
  color: string;
}

interface LucideIcon {
  kind: "icon";
  icon: Component;
  color: string;
}

type FileIconDefinition = BadgeIcon | LucideIcon;

const BADGE_ICONS: Record<string, Omit<BadgeIcon, "kind">> = {
  js: { label: "JS", color: "text-amber-500 dark:text-amber-400" },
  jsx: { label: "JS", color: "text-amber-500 dark:text-amber-400" },
  mjs: { label: "JS", color: "text-amber-500 dark:text-amber-400" },
  cjs: { label: "JS", color: "text-amber-500 dark:text-amber-400" },
  ts: { label: "TS", color: "text-blue-600 dark:text-blue-400" },
  tsx: { label: "TS", color: "text-blue-600 dark:text-blue-400" },
  mts: { label: "TS", color: "text-blue-600 dark:text-blue-400" },
  cts: { label: "TS", color: "text-blue-600 dark:text-blue-400" },
  vue: { label: "V", color: "text-emerald-600 dark:text-emerald-400" },
  go: { label: "GO", color: "text-cyan-600 dark:text-cyan-400" },
  rs: { label: "RS", color: "text-orange-600 dark:text-orange-400" },
  py: { label: "PY", color: "text-blue-600 dark:text-blue-400" },
  pyw: { label: "PY", color: "text-blue-600 dark:text-blue-400" },
  rb: { label: "RB", color: "text-red-600 dark:text-red-400" },
  php: { label: "PHP", color: "text-violet-600 dark:text-violet-400" },
  kt: { label: "KT", color: "text-violet-600 dark:text-violet-400" },
  kts: { label: "KT", color: "text-violet-600 dark:text-violet-400" },
  swift: { label: "SW", color: "text-orange-600 dark:text-orange-400" },
  dart: { label: "D", color: "text-sky-600 dark:text-sky-400" },
  c: { label: "C", color: "text-blue-600 dark:text-blue-400" },
  h: { label: "H", color: "text-violet-600 dark:text-violet-400" },
  cc: { label: "C+", color: "text-blue-600 dark:text-blue-400" },
  cpp: { label: "C+", color: "text-blue-600 dark:text-blue-400" },
  cxx: { label: "C+", color: "text-blue-600 dark:text-blue-400" },
  hpp: { label: "H+", color: "text-violet-600 dark:text-violet-400" },
  cs: { label: "C#", color: "text-violet-600 dark:text-violet-400" },
};

const ICON_GROUPS: Array<{
  extensions: Set<string>;
  icon: Component;
  color: string;
}> = [
  {
    extensions: new Set(["java", "class", "jar"]),
    icon: CoffeeIcon,
    color: "text-red-600 dark:text-red-400",
  },
  {
    extensions: new Set(["json", "jsonc", "json5"]),
    icon: FileJsonIcon,
    color: "text-amber-600 dark:text-amber-400",
  },
  {
    extensions: new Set(["md", "markdown", "mdx", "txt", "rst"]),
    icon: FileTextIcon,
    color: "text-blue-600 dark:text-blue-400",
  },
  {
    extensions: new Set(["html", "htm", "xml", "svg"]),
    icon: FileCodeIcon,
    color: "text-orange-600 dark:text-orange-400",
  },
  {
    extensions: new Set(["css", "scss", "sass", "less"]),
    icon: HashIcon,
    color: "text-sky-600 dark:text-sky-400",
  },
  {
    extensions: new Set(["yaml", "yml", "toml", "ini", "conf", "env", "properties"]),
    icon: FileCogIcon,
    color: "text-muted-foreground",
  },
  {
    extensions: new Set(["sh", "bash", "zsh", "fish", "ps1", "bat", "cmd"]),
    icon: SquareTerminalIcon,
    color: "text-emerald-600 dark:text-emerald-400",
  },
  {
    extensions: new Set(["sql", "sqlite", "sqlite3", "db"]),
    icon: DatabaseIcon,
    color: "text-indigo-600 dark:text-indigo-400",
  },
  {
    extensions: new Set(["png", "jpg", "jpeg", "gif", "webp", "ico", "bmp", "avif"]),
    icon: FileImageIcon,
    color: "text-violet-600 dark:text-violet-400",
  },
  {
    extensions: new Set(["zip", "gz", "tgz", "tar", "bz2", "xz", "7z", "rar"]),
    icon: FileArchiveIcon,
    color: "text-amber-700 dark:text-amber-400",
  },
  {
    extensions: new Set(["lock"]),
    icon: FileLockIcon,
    color: "text-muted-foreground",
  },
];

const SPECIAL_FILES: Record<string, LucideIcon> = {
  dockerfile: {
    kind: "icon",
    icon: ContainerIcon,
    color: "text-blue-600 dark:text-blue-400",
  },
  containerfile: {
    kind: "icon",
    icon: ContainerIcon,
    color: "text-blue-600 dark:text-blue-400",
  },
};

const definition = computed<FileIconDefinition>(() => {
  const filename = props.path.split(/[\\/]/).pop()?.toLowerCase() ?? "";
  const special = SPECIAL_FILES[filename];
  if (special) return special;

  const extension = filename.includes(".")
    ? filename.slice(filename.lastIndexOf(".") + 1)
    : "";
  const badge = BADGE_ICONS[extension];
  if (badge) return { kind: "badge", ...badge };

  const group = ICON_GROUPS.find(({ extensions }) => extensions.has(extension));
  if (group) {
    return { kind: "icon", icon: group.icon, color: group.color };
  }
  return {
    kind: "icon",
    icon: FileIcon,
    color: "text-muted-foreground",
  };
});
</script>

<template>
  <span
    v-if="definition.kind === 'badge'"
    :class="[
      'inline-flex size-3.5 shrink-0 items-center justify-center font-mono text-[8px] font-black leading-none tracking-normal',
      definition.color,
    ]"
    aria-hidden="true"
  >
    {{ definition.label }}
  </span>
  <component
    :is="definition.icon"
    v-else
    :class="['size-3.5 shrink-0', definition.color]"
    aria-hidden="true"
  />
</template>
