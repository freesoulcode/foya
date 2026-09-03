<script setup lang="ts">
import {
  ArrowLeftIcon,
  BlocksIcon,
  BotIcon,
  BookOpenIcon,
  BrainIcon,
  CommandIcon,
  FileJsonIcon,
  GlobeIcon,
  PaletteIcon,
  PlugZapIcon,
  ScrollTextIcon,
} from "@lucide/vue";
import { usePlatform } from "@/composables/usePlatform";
import {
  Sidebar,
  SidebarContent,
  SidebarGroup,
  SidebarGroupContent,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
} from "@/components/ui/sidebar";

export type SettingsSection =
  | "connections"
  | "rules"
  | "memory"
  | "skills"
  | "commands"
  | "hooks"
  | "mcp"
  | "web-search"
  | "agents"
  | "appearance";

defineProps<{ activeSection: SettingsSection }>();
const emit = defineEmits<{
  close: [];
  select: [section: SettingsSection];
}>();

const { isMac } = usePlatform();

const sections: Array<{ id: SettingsSection; label: string; icon: typeof PlugZapIcon }> = [
  { id: "connections", label: "连接", icon: PlugZapIcon },
  { id: "rules", label: "规则", icon: ScrollTextIcon },
  { id: "memory", label: "记忆", icon: BrainIcon },
  { id: "skills", label: "技能", icon: BookOpenIcon },
  { id: "commands", label: "命令", icon: CommandIcon },
  { id: "hooks", label: "Hooks", icon: FileJsonIcon },
  { id: "mcp", label: "MCP", icon: BlocksIcon },
  { id: "web-search", label: "联网搜索", icon: GlobeIcon },
  { id: "agents", label: "Agent", icon: BotIcon },
  { id: "appearance", label: "外观", icon: PaletteIcon },
];
</script>

<template>
  <Sidebar collapsible="none" class="border-r border-sidebar-border">
    <SidebarHeader
      data-tauri-drag-region
      class="h-12 flex-row items-center p-0"
      :class="isMac ? 'pl-[72px]' : 'pl-3'"
    />
    <SidebarContent class="p-2">
      <SidebarGroup class="p-0">
        <SidebarGroupContent>
          <SidebarMenu>
            <SidebarMenuItem>
              <SidebarMenuButton @click="emit('close')">
                <ArrowLeftIcon />
                <span>返回工作区</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarGroupContent>
      </SidebarGroup>
      <SidebarGroup class="p-0 pt-4">
        <SidebarGroupContent>
          <SidebarMenu>
            <SidebarMenuItem v-for="item in sections" :key="item.id">
              <SidebarMenuButton
                :is-active="activeSection === item.id"
                @click="emit('select', item.id)"
              >
                <component :is="item.icon" />
                <span>{{ item.label }}</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarGroupContent>
      </SidebarGroup>
    </SidebarContent>
  </Sidebar>
</template>
