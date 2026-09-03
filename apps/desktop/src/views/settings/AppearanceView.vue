<script setup lang="ts">
import {
  ExternalLinkIcon,
  GlobeIcon,
  MonitorIcon,
  MoonIcon,
  SunIcon,
} from "@lucide/vue";
import { useTheme, type Theme } from "@/composables/useTheme";
import {
  useLinkPreference,
  type LinkOpenMode,
} from "@/composables/useLinkPreference";
import SettingsPage from "@/layouts/settings/SettingsPage.vue";
import { Button } from "@/components/ui/button";
import { ButtonGroup } from "@/components/ui/button-group";

const { theme, setTheme } = useTheme();
const { linkOpenMode, setLinkOpenMode } = useLinkPreference();

const themeOptions: Array<{ value: Theme; label: string; icon: typeof SunIcon }> = [
  { value: "system", label: "跟随系统", icon: MonitorIcon },
  { value: "light", label: "浅色", icon: SunIcon },
  { value: "dark", label: "深色", icon: MoonIcon },
];
const linkOpenOptions: Array<{
  value: LinkOpenMode;
  label: string;
  icon: typeof GlobeIcon;
}> = [
  { value: "workbar", label: "内置浏览器", icon: GlobeIcon },
  { value: "system", label: "系统浏览器", icon: ExternalLinkIcon },
];
</script>

<template>
  <SettingsPage
    title="外观"
    description="调整界面主题与链接打开方式。"
  >
    <div class="max-w-4xl">
      <div class="flex items-center justify-between border-b border-border py-3">
        <span class="text-sm font-medium">主题</span>
        <ButtonGroup aria-label="主题">
          <Button
            v-for="option in themeOptions"
            :key="option.value"
            variant="outline"
            size="sm"
            :class="[
              'min-w-20 shadow-none',
              theme === option.value
                ? 'bg-accent text-accent-foreground'
                : 'text-muted-foreground',
            ]"
            :aria-pressed="theme === option.value"
            @click="setTheme(option.value)"
          >
            <component :is="option.icon" />
            {{ option.label }}
          </Button>
        </ButtonGroup>
      </div>
      <div class="flex items-center justify-between gap-6 border-b border-border py-3">
        <span class="text-sm font-medium">链接打开方式</span>
        <ButtonGroup aria-label="链接打开方式">
          <Button
            v-for="option in linkOpenOptions"
            :key="option.value"
            variant="outline"
            size="sm"
            :class="[
              'min-w-24 shadow-none',
              linkOpenMode === option.value
                ? 'bg-accent text-accent-foreground'
                : 'text-muted-foreground',
            ]"
            :aria-pressed="linkOpenMode === option.value"
            @click="setLinkOpenMode(option.value)"
          >
            <component :is="option.icon" />
            {{ option.label }}
          </Button>
        </ButtonGroup>
      </div>
    </div>
  </SettingsPage>
</template>
