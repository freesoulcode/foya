import { computed } from "vue";

export type Platform = "macos" | "windows" | "linux" | "other";

function detectPlatform(): Platform {
  if (typeof navigator === "undefined") return "other";
  const ua = navigator.userAgent.toLowerCase();
  if (ua.includes("mac os x")) return "macos";
  if (ua.includes("windows")) return "windows";
  if (ua.includes("linux")) return "linux";
  return "other";
}

const platform = detectPlatform();

export function usePlatform() {
  const isMac = computed(() => platform === "macos");
  const isWindows = computed(() => platform === "windows");
  const isLinux = computed(() => platform === "linux");
  const showCustomWindowControls = computed(
    () => platform === "windows" || platform === "linux"
  );
  return { platform, isMac, isWindows, isLinux, showCustomWindowControls };
}
